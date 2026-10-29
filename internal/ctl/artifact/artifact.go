// Package artifact prepares bounded deployment snapshots and their public proof.
// Credential checks report rules and paths, never matched content.
package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ozal-project/ozal/internal/pages"
)

type Proof struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Snapshot struct {
	Archive []byte
	Proof   Proof
}

type rule struct {
	name       string
	expression *regexp.Regexp
}

func contentRules() []rule {
	patterns := [][2]string{
		{"private_key", `-----BEGIN [A-Z ]*PRIVATE KEY-----`},
		{"github_token", `(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36}`},
		{"github_pat", `github_pat_[A-Za-z0-9_]{22,}`},
		{"aws_access_key", `AKIA[0-9A-Z]{16}`},
		{"slack_token", `xox[abprs]-[A-Za-z0-9-]{10,}`},
		{"stripe_live_key", `(sk|rk)_live_[A-Za-z0-9]{16,}`},
		{"google_api_key", `AIza[0-9A-Za-z_-]{35}`},
	}
	out := make([]rule, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, rule{p[0], regexp.MustCompile(p[1])})
	}
	return out
}

func forbidden(name string) bool {
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".git" || part == ".npmrc" || part == ".env" || strings.HasPrefix(part, ".env.") ||
			strings.HasPrefix(part, "id_rsa") || strings.HasPrefix(part, "id_ed25519") || strings.HasSuffix(part, ".key") || strings.HasSuffix(part, ".pem") {
			return true
		}
	}
	return false
}

func refusal(name, code string, rules []rule) error {
	for _, r := range rules {
		name = r.expression.ReplaceAllString(name, "[redacted]")
	}
	return fmt.Errorf("artifact refused: %s (%q)", code, name)
}

// FromDir refuses forbidden names before reading them, then checks the exact
// packed snapshot as well. A subsequent source edit cannot change the upload.
func FromDir(dir string) (Snapshot, error) {
	rules := contentRules()
	if err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return errors.New("cannot inspect build output")
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return errors.New("cannot inspect build output")
		}
		if forbidden(rel) {
			return refusal(rel, "secret_file", rules)
		}
		return nil
	}); err != nil {
		return Snapshot{}, err
	}
	data, err := pages.Pack(dir)
	if err != nil {
		return Snapshot{}, errors.New("build output cannot be packed safely")
	}
	return FromArchive(data)
}

func ReadArchive(path string) (Snapshot, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > pages.MaxUploadBytes {
		return Snapshot{}, errors.New("archive must be a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return Snapshot{}, errors.New("cannot open deployment archive")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return Snapshot{}, errors.New("archive changed during review")
	}
	data, err := io.ReadAll(io.LimitReader(f, pages.MaxUploadBytes+1))
	if err != nil {
		return Snapshot{}, errors.New("cannot read deployment archive")
	}
	return FromArchive(data)
}

func FromArchive(data []byte) (Snapshot, error) {
	if len(data) == 0 || len(data) > pages.MaxUploadBytes {
		return Snapshot{}, errors.New("deployment archive exceeds the upload limit or is empty")
	}
	dir, err := os.MkdirTemp("", "ozal-artifact-")
	if err != nil {
		return Snapshot{}, errors.New("cannot prepare private archive validation")
	}
	defer os.RemoveAll(dir)
	if _, err := pages.Extract(bytes.NewReader(data), dir, pages.DefaultLimits); err != nil {
		return Snapshot{}, errors.New("deployment archive failed entry, size or path validation")
	}
	rules := contentRules()
	var assets []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("cannot inspect deployment snapshot")
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return errors.New("cannot inspect deployment snapshot")
		}
		if forbidden(rel) {
			return refusal(rel, "secret_file", rules)
		}
		if d.IsDir() {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return errors.New("cannot inspect deployment content")
		}
		if !bytes.Contains(body, []byte{0}) {
			for _, rule := range rules {
				if rule.expression.Match(body) {
					return refusal(rel, rule.name, rules)
				}
			}
		}
		if rel != "_headers" && rel != "_redirects" && rel != "index.html" {
			assets = append(assets, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	sort.Strings(assets)
	asset := "index.html"
	if len(assets) > 0 {
		asset = assets[0]
	}
	for _, p := range assets {
		if strings.HasSuffix(p, ".css") || strings.HasSuffix(p, ".js") {
			asset = p
			break
		}
	}
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(asset)))
	if err != nil {
		return Snapshot{}, errors.New("deployment has no readable public proof")
	}
	sum := sha256.Sum256(body)
	return Snapshot{Archive: data, Proof: Proof{Path: asset, SHA256: hex.EncodeToString(sum[:])}}, nil
}

func WriteNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("output must be a new writable file")
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("output could not be written completely")
	}
	return nil
}
