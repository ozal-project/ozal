package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/release"
)

// runRelease dispatches `ozal release keygen|sign`: the two steps a project
// needs to publish releases that edges update to on their own.
func runRelease(_ context.Context, args []string, out io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "keygen":
			return runReleaseKeygen(args[1:], out)
		case "sign":
			return runReleaseSign(args[1:], out)
		case "latest":
			return runReleaseLatest(args[1:], out)
		case "catalog":
			return runReleaseCatalog(args[1:], out, time.Now)
		}
	}
	return errors.New("usage: ozal release keygen|sign|latest|catalog [flags]")
}

// runReleaseKeygen writes a new signing key and prints the public key edges
// pin. The private key file is created, never replaced: losing the old key by
// accident would strand every edge that pins it.
func runReleaseKeygen(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("release keygen", flag.ContinueOnError)
	fs.SetOutput(out)
	path := fs.String("key", "", "file to create with the private signing key (mode 0600)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || fs.NArg() != 0 {
		return errors.New("usage: ozal release keygen -key FILE")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", *path, err)
	}
	if _, err := fmt.Fprintln(f, base64.StdEncoding.EncodeToString(priv.Seed())); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(out, "signing key written to %s: store it as the OZAL_RELEASE_KEY secret of the release workflow, then delete this file\n", *path)
	fmt.Fprintf(out, "public key (pin it on every edge, e.g. /etc/ozal/release.pub):\n%s\n", base64.StdEncoding.EncodeToString(pub))
	return nil
}

// runReleaseSign signs a checksums file. The key is an env: or file:
// reference, so CI never puts it on a command line.
func runReleaseSign(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("release sign", flag.ContinueOnError)
	fs.SetOutput(out)
	keyRef := fs.String("key", "env:OZAL_RELEASE_KEY", "env: or file: reference to the private signing key")
	in := fs.String("in", release.ChecksumsName, "checksums file to sign")
	outPath := fs.String("out", release.SignatureName, "signature file to write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if !strings.HasPrefix(*keyRef, "env:") && !strings.HasPrefix(*keyRef, "file:") {
		return errors.New("-key must be an env: or file: reference; a key on the command line ends up in shell history and process lists")
	}
	text, err := config.ResolveSecret(*keyRef)
	if err != nil {
		return fmt.Errorf("signing key: %w", err)
	}
	key, err := release.ParsePrivateKey(text)
	if err != nil {
		return err
	}
	checksums, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, []byte(release.Sign(checksums, key)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "signed %s -> %s\n", *in, *outPath)
	return nil
}

// runReleaseLatest prints the release a channel offers and the one before it
// in that channel ("TAG PREVIOUS", PREVIOUS may be absent), from the release
// list the GitHub API returns. It prints nothing when the channel has no
// release yet, so a publisher can skip that channel.
func runReleaseLatest(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("release latest", flag.ContinueOnError)
	fs.SetOutput(out)
	list := fs.String("releases", "", "file with the GitHub release list (GET /repos/OWNER/REPO/releases)")
	channel := fs.String("channel", "", "stable or preview")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *list == "" || fs.NArg() != 0 {
		return errors.New("usage: ozal release latest -releases FILE -channel stable|preview")
	}
	raw, err := os.ReadFile(*list)
	if err != nil {
		return err
	}
	releases, err := release.ParseGitHubReleases(raw)
	if err != nil {
		return err
	}
	latest, previous, ok, err := release.ChannelRelease(releases, *channel)
	if err != nil || !ok {
		return err
	}
	if previous != nil {
		_, err = fmt.Fprintf(out, "%s %s\n", latest.Tag, previous.Tag)
	} else {
		_, err = fmt.Fprintln(out, latest.Tag)
	}
	return err
}

// runReleaseCatalog writes the signed per-platform catalogs edges read
// (`ozal update -catalog`, the update executor's catalog_base). It reads the
// published release, its checksums and their signature, proves the checksums
// with the public half of the signing key, and writes
// OUT/<channel>/<os>/<arch> and .sig for every tar.gz archive. Running it
// again later re-signs the same release with a new window and a higher
// sequence.
func runReleaseCatalog(args []string, out io.Writer, now func() time.Time) error {
	fs := flag.NewFlagSet("release catalog", flag.ContinueOnError)
	fs.SetOutput(out)
	keyRef := fs.String("key", "env:OZAL_RELEASE_KEY", "env: or file: reference to the private signing key")
	channel := fs.String("channel", "", "stable or preview")
	releasePath := fs.String("release", "", "file with the GitHub release object (GET /repos/OWNER/REPO/releases/tags/TAG)")
	checksumsPath := fs.String("checksums", release.ChecksumsName, "the release's checksums file")
	signaturePath := fs.String("signature", release.SignatureName, "the release's checksums signature")
	commit := fs.String("commit", "", "full commit the release tag points to")
	outDir := fs.String("out", "", "catalog base directory to write into (for example site/dist/api/v1/releases)")
	ttl := fs.Duration("ttl", 14*24*time.Hour, "how long the signed catalogs stay valid")
	migrations := fs.Bool("migrations", false, "the release may migrate the state database")
	minVersion := fs.String("min-version", "", "oldest installed release that may update directly to this one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *channel == "" || *releasePath == "" || *commit == "" || *outDir == "" || fs.NArg() != 0 {
		return errors.New("usage: ozal release catalog -channel stable|preview -release FILE -commit SHA -out DIR [-checksums FILE] [-signature FILE] [-ttl DURATION] [-migrations] [-min-version VERSION]")
	}
	if !strings.HasPrefix(*keyRef, "env:") && !strings.HasPrefix(*keyRef, "file:") {
		return errors.New("-key must be an env: or file: reference; a key on the command line ends up in shell history and process lists")
	}
	text, err := config.ResolveSecret(*keyRef)
	if err != nil {
		return fmt.Errorf("signing key: %w", err)
	}
	key, err := release.ParsePrivateKey(text)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*releasePath)
	if err != nil {
		return err
	}
	published, err := release.ParseGitHubRelease(raw)
	if err != nil {
		return err
	}
	checksums, err := os.ReadFile(*checksumsPath)
	if err != nil {
		return err
	}
	signature, err := os.ReadFile(*signaturePath)
	if err != nil {
		return err
	}
	sums, err := release.VerifyChecksums(checksums, string(signature), key.Public().(ed25519.PublicKey))
	if err != nil {
		return fmt.Errorf("%s of %s: %w", release.ChecksumsName, published.Tag, err)
	}
	files, err := release.BuildCatalogs(release.CatalogRequest{
		Channel: *channel, Release: published, Commit: *commit, Checksums: sums,
		GeneratedAt: now(), TTL: *ttl, Migrations: *migrations, MinVersion: *minVersion,
	}, key)
	if err != nil {
		return err
	}
	for _, f := range files {
		target := filepath.Join(*outDir, filepath.FromSlash(f.Path(*channel)))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, f.Body, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(target+release.CatalogSignatureSuffix, []byte(f.Signature+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s -> %s\n", *channel, published.Tag, f.Path(*channel))
	}
	return nil
}
