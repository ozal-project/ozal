package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/pages"
)

// runSite implements `switchboard site deploy|rollback|list` (PAGES-002): a
// client of the admin API, so it works against a local process or, through
// an SSH tunnel, against a remote edge.
func runSite(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: switchboard site deploy|rollback|list [flags]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("site "+sub, flag.ContinueOnError)
	fs.SetOutput(out)
	adminURL := fs.String("admin", "http://"+config.DefaultAdminListen, "admin API base URL (http only on loopback)")
	tokenRef := fs.String("token", "env:SWITCHBOARD_ADMIN_TOKEN", "admin token reference (env:NAME or file:/path)")
	site := fs.String("site", "", "site name")
	to := fs.String("to", "", "rollback: deployment id to make live")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	base, err := adminBase(*adminURL)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Minute}

	switch sub {
	case "list":
		var listed struct {
			Sites []pages.Site `json:"sites"`
		}
		if err := call(ctx, client, http.MethodGet, base+"/_/api/sites", "", "", nil, &listed); err != nil {
			return err
		}
		for _, s := range listed.Sites {
			fmt.Fprintf(out, "%s\n", s.Name)
			for _, d := range s.Deployments {
				marker := " "
				if d.Live {
					marker = "*"
				}
				fmt.Fprintf(out, "  %s %s  %s  %d files  %d bytes\n", marker, d.ID, d.CreatedAt.Format(time.RFC3339), d.Files, d.Bytes)
			}
		}
		return nil
	case "deploy", "rollback":
	default:
		return fmt.Errorf("unknown site command %q", sub)
	}

	if !pages.ValidSiteName(*site) {
		return pages.ErrBadSiteName
	}
	token, err := config.ResolveSecret(*tokenRef)
	if err != nil || token == "" {
		return fmt.Errorf("admin token: set %s or pass -token file:/path", *tokenRef)
	}
	var result pages.Deployment
	if sub == "rollback" {
		body, _ := json.Marshal(map[string]string{"id": *to})
		if err := call(ctx, client, http.MethodPost, base+"/_/api/sites/"+*site+"/rollback", token, "application/json", bytes.NewReader(body), &result); err != nil {
			return err
		}
		fmt.Fprintf(out, "rolled back %s to %s\n", *site, result.ID)
		return nil
	}
	if fs.NArg() != 1 {
		return errors.New("usage: switchboard site deploy -site NAME DIR")
	}
	archive, err := packSite(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := call(ctx, client, http.MethodPost, base+"/_/api/sites/"+*site+"/deployments", token, "application/gzip", bytes.NewReader(archive), &result); err != nil {
		return err
	}
	fmt.Fprintf(out, "deployed %s %s (%d files, %d bytes) — live\n", *site, result.ID, result.Files, result.Bytes)
	return nil
}

// adminBase accepts https anywhere and http only on loopback: the bearer
// token must never cross a network in clear text.
func adminBase(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("-admin %q must be an http(s) URL", raw)
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if !strings.EqualFold(u.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
			return "", fmt.Errorf("-admin %q: plain http only on loopback (use an SSH tunnel for a remote edge)", raw)
		}
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

func call(ctx context.Context, client *http.Client, method, target, token, contentType string, body io.Reader, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var problem struct{ Error, Code string }
		_ = json.Unmarshal(payload, &problem)
		if problem.Code != "" {
			return fmt.Errorf("%s: %s (%s)", resp.Status, problem.Error, problem.Code)
		}
		return fmt.Errorf("%s: %s", resp.Status, problem.Error)
	}
	return json.Unmarshal(payload, result)
}

// packSite builds the tar.gz of a build directory. It refuses symlinks and
// special files in the source instead of following them: the archive must
// contain exactly what the directory holds, nothing it points to.
func packSite(dir string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			return err
		}
		name := filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink; deployments contain regular files only", name)
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o755})
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", name)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: info.Size()}); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", dir, err)
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > pages.MaxUploadBytes {
		return nil, fmt.Errorf("pack %s: archive is %d bytes, over the %d-byte upload cap", dir, buf.Len(), pages.MaxUploadBytes)
	}
	return buf.Bytes(), nil
}
