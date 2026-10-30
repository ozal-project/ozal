package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func releaseArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zip := gzip.NewWriter(&out)
	tr := tar.NewWriter(zip)
	if err := tr.WriteHeader(&tar.Header{Name: "ozal", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestFetchBinaryRequiresValidSignatureAndArchiveHash(t *testing.T) {
	pub, priv := keys(t)
	binary := []byte("signed candidate")
	archive := releaseArchive(t, binary)
	archiveSum := sha256.Sum256(archive)
	name := ArchiveName("v0.2.0", "linux", "amd64")
	checksums := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(archiveSum[:]), name))
	signature := Sign(checksums, priv)
	badSignature := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/ozal-project/ozal/releases/tags/v0.2.0":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.2.0", "assets": []map[string]string{
				{"name": ChecksumsName, "browser_download_url": serverURL(r, "/checksums")},
				{"name": SignatureName, "browser_download_url": serverURL(r, "/signature")},
				{"name": name, "browser_download_url": serverURL(r, "/archive")},
			}})
		case "/checksums":
			_, _ = w.Write(checksums)
		case "/signature":
			if badSignature {
				_, _ = w.Write([]byte("bad"))
			} else {
				_, _ = w.Write([]byte(signature))
			}
		case "/archive":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	got, gotHash, err := FetchBinary(context.Background(), server.Client(), server.URL+"/repos/ozal-project/ozal", "v0.2.0", "linux", "amd64", pub)
	if err != nil || !bytes.Equal(got, binary) {
		t.Fatalf("verified fetch = %q, %v", got, err)
	}
	want := sha256.Sum256(binary)
	if gotHash != hex.EncodeToString(want[:]) {
		t.Fatalf("binary hash %s", gotHash)
	}
	badSignature = true
	if _, _, err := FetchBinary(context.Background(), server.Client(), server.URL+"/repos/ozal-project/ozal", "v0.2.0", "linux", "amd64", pub); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("tampered signature accepted: %v", err)
	}
}

func serverURL(r *http.Request, path string) string { return "http://" + r.Host + path }
