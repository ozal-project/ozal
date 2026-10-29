package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func release(t *testing.T, kind byte, duplicate bool) (string, string) {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	count := 1
	if duplicate {
		count = 2
	}
	for i := 0; i < count; i++ {
		h := &tar.Header{Name: "ozal", Typeflag: kind, Mode: 0755, Linkname: "/bin/sh"}
		if kind == tar.TypeReg {
			h.Size = 6
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			tw.Write([]byte("binary"))
		}
	}
	tw.Close()
	gz.Close()
	path := filepath.Join(t.TempDir(), "release.tgz")
	os.WriteFile(path, b.Bytes(), 0600)
	sum := sha256.Sum256(b.Bytes())
	return path, hex.EncodeToString(sum[:])
}
func TestBinaryChecksumNoOverwriteAndArchiveSafety(t *testing.T) {
	for _, tc := range []struct {
		name           string
		kind           byte
		duplicate, bad bool
	}{{"valid", tar.TypeReg, false, false}, {"bad checksum", tar.TypeReg, false, true}, {"link", tar.TypeSymlink, false, false}, {"duplicate", tar.TypeReg, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			archive, sum := release(t, tc.kind, tc.duplicate)
			if tc.bad {
				sum = strings.Repeat("0", 64)
			}
			dest := filepath.Join(t.TempDir(), "ozal")
			err := Binary(io.Discard, BinaryRequest{Archive: archive, SHA256: sum, Destination: dest, Apply: true})
			valid := tc.kind == tar.TypeReg && !tc.duplicate && !tc.bad
			if (err == nil) != valid {
				t.Fatal(err)
			}
			if valid {
				if err := Binary(io.Discard, BinaryRequest{Archive: archive, SHA256: sum, Destination: dest, Apply: true}); err == nil {
					t.Fatal("overwrote binary")
				}
			} else {
				if _, err := os.Lstat(dest); !os.IsNotExist(err) {
					t.Fatal("published invalid binary")
				}
			}
		})
	}
}
func TestServiceStagingPlanPermissionsAndLegacyRefusal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux installer")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "stage")
	req := ServiceRequest{Binary: exe, Root: root, Target: "http://127.0.0.1:4000", Group: "staff"}
	if err := Service(context.Background(), io.Discard, nil, req); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("plan wrote files")
	}
	req.Apply = true
	var out bytes.Buffer
	if err := Service(context.Background(), &out, nil, req); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{"etc/ozal/admin.token": 0640, "var/lib/ozal": 0700, "var/lib/ozal/runtime.json": 0600, "usr/local/lib/ozal/ozal": 0755} {
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", path, info, err)
		}
	}
	token, _ := os.ReadFile(filepath.Join(root, "etc/ozal/admin.token"))
	cfg, _ := os.ReadFile(filepath.Join(root, "etc/ozal/config.json"))
	if bytes.Contains(cfg, bytes.TrimSpace(token)) || strings.Contains(out.String(), string(bytes.TrimSpace(token))) {
		t.Fatal("secret exposed")
	}
	if err := Service(context.Background(), io.Discard, nil, req); err == nil {
		t.Fatal("overwrote service")
	}
	legacy := t.TempDir()
	os.MkdirAll(filepath.Join(legacy, "etc/switchboard"), 0700)
	os.WriteFile(filepath.Join(legacy, "etc/switchboard/config.json"), nil, 0600)
	req.Root = legacy
	if err := Service(context.Background(), io.Discard, nil, req); err == nil {
		t.Fatal("shadowed old state")
	}
	if _, err := os.Stat(filepath.Join(legacy, "etc/ozal")); !os.IsNotExist(err) {
		t.Fatal("changed legacy installation")
	}
	docs, err := os.ReadFile("../../docs/operations/ozal.service")
	if err == nil && string(docs) != Unit() {
		t.Fatal("documented and installed unit differ")
	}
}

func TestBinaryRejectsCorruptGzipFooterBeforePublishing(t *testing.T) {
	archive, _ := release(t, tar.TypeReg, false)
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-8] ^= 0xff
	if err := os.WriteFile(archive, data, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	dest := filepath.Join(t.TempDir(), "ozal")
	if err := Binary(io.Discard, BinaryRequest{Archive: archive, SHA256: hex.EncodeToString(sum[:]), Destination: dest, Apply: true}); err == nil {
		t.Fatal("published corrupt gzip")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("invalid binary became visible")
	}
}
