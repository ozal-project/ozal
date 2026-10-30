// Package release decides whether an installed Ozal should move to a newer
// published release, and proves that what it would install is what the
// project signed. It is pure: fetching, extracting and installing belong to
// the caller.
//
// Trust: the release workflow signs the checksums file GoReleaser writes
// (one "sha256  name" line per archive) with an Ed25519 key that exists only
// as a CI secret. An edge pins the public key. A signed checksums file binds
// each archive name, and so its version, to its hash; the archive is then
// checked against that hash before anything is extracted.
package release

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ChecksumsName and SignatureName are the release assets an update reads.
const (
	ChecksumsName = "checksums.txt"
	SignatureName = "checksums.txt.sig"
)

// ParsePublicKey reads a base64 Ed25519 public key, as `ozal release keygen`
// prints it.
func ParsePublicKey(text string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("release public key must be a base64 Ed25519 public key")
	}
	return ed25519.PublicKey(raw), nil
}

// ParsePrivateKey reads a base64 Ed25519 seed, as `ozal release keygen` writes it.
func ParsePrivateKey(text string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(raw) != ed25519.SeedSize {
		return nil, errors.New("release signing key must be a base64 Ed25519 seed")
	}
	return ed25519.NewKeyFromSeed(raw), nil
}

// Sign returns the base64 signature of a checksums file.
func Sign(checksums []byte, key ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, checksums))
}

// ErrBadSignature means the checksums file was not signed by the pinned key.
var ErrBadSignature = errors.New("release checksums are not signed by the pinned key")

// VerifyChecksums checks the signature before reading a single line, and
// returns the archive hashes it vouches for.
func VerifyChecksums(checksums []byte, signature string, key ed25519.PublicKey) (map[string]string, error) {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(key, checksums, sig) {
		return nil, ErrBadSignature
	}
	sums := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(checksums))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 || name == "" || strings.ContainsAny(name, "/\\") {
			return nil, fmt.Errorf("malformed checksums line %q", line)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("malformed checksum for %s", name)
		}
		if _, dup := sums[name]; dup {
			return nil, fmt.Errorf("checksums list %s twice", name)
		}
		sums[name] = strings.ToLower(sum)
	}
	return sums, scanner.Err()
}

// ArchiveName is the GoReleaser archive for one platform (see .goreleaser.yaml).
func ArchiveName(version, goos, goarch string) string {
	return fmt.Sprintf("ozal_%s_%s_%s.tar.gz", strings.TrimPrefix(version, "v"), goos, goarch)
}

// Published is a release as the hosting service lists it.
type Published struct {
	Tag        string
	Draft      bool
	Prerelease bool
}

// Decision codes. Only CodeUpdate leads to an install.
const (
	CodeUpdate      = "update"      // a newer release exists
	CodeCurrent     = "current"     // nothing newer
	CodeUnversioned = "unversioned" // the installed build is not a release; refuse to guess
)

// Decision is what an update run should do, with a reason for the log.
type Decision struct {
	Code   string
	Target string // tag to install when Code is CodeUpdate
	Reason string
}

// Decide picks the highest published release newer than installed. Drafts are
// never candidates; prereleases only when allowed. An installed build that is
// not a release version (a "dev" build) is never replaced automatically: the
// newest release could be older than what is running. force allows that one
// move from an unversioned build.
func Decide(installed string, releases []Published, allowPrerelease, force bool) Decision {
	best := ""
	for _, r := range releases {
		if r.Draft || (r.Prerelease && !allowPrerelease) || !valid(r.Tag) {
			continue
		}
		if best == "" || Compare(r.Tag, best) > 0 {
			best = r.Tag
		}
	}
	if !valid(installed) {
		if force && best != "" {
			return Decision{CodeUpdate, best, fmt.Sprintf("installed build %q is not a release; moving to %s as asked", installed, best)}
		}
		return Decision{Code: CodeUnversioned, Reason: fmt.Sprintf("installed build %q is not a release, so a newer one cannot be told apart from an older one; run the update once by hand", installed)}
	}
	if best == "" || Compare(best, installed) <= 0 {
		return Decision{Code: CodeCurrent, Reason: "no published release is newer than " + installed}
	}
	return Decision{CodeUpdate, best, fmt.Sprintf("%s is newer than %s", best, installed)}
}

// version is a parsed semantic version: v?MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD].
type version struct {
	core [3]int
	pre  []string
}

func parse(v string) (version, bool) {
	v = strings.TrimPrefix(v, "v")
	if strings.HasPrefix(v, "v") {
		return version{}, false
	}
	if coreAndPre, build, hasBuild := strings.Cut(v, "+"); hasBuild {
		if build == "" || strings.Contains(build, "+") || !validIdentifiers(build, false) {
			return version{}, false
		}
		v = coreAndPre
	}
	core, pre, hasPre := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 || (hasPre && pre == "") {
		return version{}, false
	}
	var out version
	for i, p := range parts {
		if p == "" || !digitsOnly(p) {
			return version{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return version{}, false
		}
		out.core[i] = n
	}
	if hasPre {
		if !validIdentifiers(pre, true) {
			return version{}, false
		}
		out.pre = strings.Split(pre, ".")
	}
	return out, true
}

func digitsOnly(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func validIdentifiers(value string, prerelease bool) bool {
	for _, id := range strings.Split(value, ".") {
		if id == "" {
			return false
		}
		numeric := true
		for _, r := range id {
			if !((r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '-') {
				return false
			}
			if r < '0' || r > '9' {
				numeric = false
			}
		}
		if prerelease && numeric && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

func valid(v string) bool { _, ok := parse(v); return ok }

// IsVersion reports whether value is a valid semantic release tag.
func IsVersion(value string) bool { return valid(value) }

// Compare orders two versions by semantic-versioning precedence: -1, 0 or 1.
// Invalid versions sort below every valid one.
func Compare(a, b string) int {
	va, okA := parse(a)
	vb, okB := parse(b)
	switch {
	case !okA && !okB:
		return 0
	case !okA:
		return -1
	case !okB:
		return 1
	}
	for i := range 3 {
		if c := cmpInt(va.core[i], vb.core[i]); c != 0 {
			return c
		}
	}
	// A release outranks its prereleases.
	switch {
	case len(va.pre) == 0 && len(vb.pre) == 0:
		return 0
	case len(va.pre) == 0:
		return 1
	case len(vb.pre) == 0:
		return -1
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		if c := cmpIdentifier(va.pre[i], vb.pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(va.pre), len(vb.pre))
}

func cmpIdentifier(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return cmpInt(na, nb)
	case errA == nil:
		return -1 // numeric identifiers have lower precedence
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
