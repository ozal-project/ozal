package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
)

const maxSignedReleaseArchive = 512 << 20

// FetchBinary downloads one explicitly requested GitHub release and verifies
// its signed checksums before extracting only the ozal executable.
func FetchBinary(ctx context.Context, client *http.Client, api, tag, goos, goarch string, key ed25519.PublicKey) ([]byte, string, error) {
	if !valid(tag) || goos == "" || goarch == "" || len(key) != ed25519.PublicKeySize {
		return nil, "", errors.New("a semantic release tag, platform and pinned release key are required")
	}
	if client == nil {
		client = &http.Client{}
	}
	base, err := safeAPI(api)
	if err != nil {
		return nil, "", err
	}
	var published struct {
		Tag    string `json:"tag_name"`
		Draft  bool   `json:"draft"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := fetchJSON(ctx, client, base+"/releases/tags/"+url.PathEscape(tag), 4<<20, &published); err != nil {
		return nil, "", err
	}
	if published.Draft || published.Tag != tag {
		return nil, "", errors.New("requested release is missing, draft or has a different tag")
	}
	asset := func(name string) (string, error) {
		for _, a := range published.Assets {
			if a.Name == name {
				if !safeReleaseURL(a.URL) {
					return "", errors.New("release asset URL is not an approved HTTPS host")
				}
				return a.URL, nil
			}
		}
		return "", fmt.Errorf("release %s has no %s", tag, name)
	}
	sumsURL, err := asset(ChecksumsName)
	if err != nil {
		return nil, "", err
	}
	sigURL, err := asset(SignatureName)
	if err != nil {
		return nil, "", err
	}
	sumsRaw, err := fetchBytes(ctx, client, sumsURL, 64<<10)
	if err != nil {
		return nil, "", err
	}
	sigRaw, err := fetchBytes(ctx, client, sigURL, 1<<10)
	if err != nil {
		return nil, "", err
	}
	sums, err := VerifyChecksums(sumsRaw, string(sigRaw), key)
	if err != nil {
		return nil, "", err
	}
	archiveName := ArchiveName(tag, goos, goarch)
	want, ok := sums[archiveName]
	if !ok {
		return nil, "", fmt.Errorf("signed checksums do not list %s", archiveName)
	}
	archiveURL, err := asset(archiveName)
	if err != nil {
		return nil, "", err
	}
	archive, err := fetchBytes(ctx, client, archiveURL, maxSignedReleaseArchive)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != want {
		return nil, "", errors.New("release archive does not match its signed checksum")
	}
	binary, err := releaseBinary(archive)
	if err != nil {
		return nil, "", err
	}
	binarySum := sha256.Sum256(binary)
	return binary, hex.EncodeToString(binarySum[:]), nil
}

func safeAPI(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" || !safeReleaseURL(raw) {
		return "", errors.New("release API must use an approved HTTPS GitHub endpoint")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

func safeReleaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" {
		return false
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
		return u.Scheme == "http" && ip.IsLoopback()
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if u.Scheme == "http" {
		return host == "localhost" || strings.HasSuffix(host, ".localhost")
	}
	if u.Scheme != "https" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	return host == "api.github.com" || host == "github.com" || host == "githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

func fetchJSON(ctx context.Context, client *http.Client, raw string, limit int64, dst any) error {
	body, err := fetchBytes(ctx, client, raw, limit)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("release metadata is invalid: %w", err)
	}
	return nil
}

func fetchBytes(ctx context.Context, client *http.Client, raw string, limit int64) ([]byte, error) {
	if !safeReleaseURL(raw) {
		return nil, errors.New("release URL is not an approved HTTPS host")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, application/octet-stream")
	request.Header.Set("User-Agent", "ozal-release-fetch")
	safe := *client
	priorRedirect := client.CheckRedirect
	safe.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !safeReleaseURL(next.URL.String()) {
			return errors.New("release redirect used an unapproved host")
		}
		for _, prev := range via {
			if prev.URL.Scheme == "https" && next.URL.Scheme != "https" {
				return errors.New("release redirect downgraded HTTPS")
			}
		}
		if priorRedirect != nil {
			return priorRedirect(next, via)
		}
		return nil
	}
	response, err := safe.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release download failed: %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("release response exceeds its size limit")
	}
	return body, nil
}

func releaseBinary(archive []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	total := int64(0)
	var found []byte
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Size < 0 || header.Size > maxSignedReleaseArchive || total > maxSignedReleaseArchive-header.Size {
			return nil, errors.New("release archive expands beyond its size limit")
		}
		total += header.Size
		name := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(header.Name, "./")))
		if name != "ozal" {
			continue
		}
		if found != nil || header.Typeflag != tar.TypeReg || header.Size == 0 {
			return nil, errors.New("release archive has an invalid ozal entry")
		}
		found, err = io.ReadAll(io.LimitReader(tarReader, maxSignedReleaseArchive+1))
		if err != nil || int64(len(found)) != header.Size {
			return nil, errors.New("release binary is truncated or oversized")
		}
	}
	if found == nil {
		return nil, errors.New("release archive contains no ozal executable")
	}
	return found, nil
}
