// Package pages stores static-site deployments: archives uploaded through the
// admin API are checked entry by entry, extracted into immutable directories
// and made live with one atomic switch (docs/engineering.md#publicacion).
//
// CheckEntry is the pure core: it judges one archive entry with no I/O.
// Extract is the adapter that applies it to a tar.gz stream.
package pages

import (
	"path"
	"strings"
)

// Code is a stable decision code; it appears in events and API errors.
type Code string

const (
	EntryAllowed Code = "entry.allowed"
	// UnsafePath: empty, absolute, a drive letter, a backslash, a NUL byte
	// or a ".." component anywhere — anything that could leave the site.
	UnsafePath Code = "entry.unsafe_path"
	// Link: symbolic and hard links are never extracted.
	Link Code = "entry.link"
	// Type: devices, FIFOs and anything that is not a file or directory.
	Type Code = "entry.type"
	// TooLarge: a negative size or more than Limits.MaxFileBytes.
	TooLarge Code = "entry.too_large"
	// TooDeep: more path components than Limits.MaxDepth.
	TooDeep Code = "entry.too_deep"
	// SecretFile: a name that only ever holds secrets or repository internals
	// (.env files, .git, .npmrc, private SSH keys). Whatever is deployed is
	// public, so a build that ships one is refused rather than served.
	SecretFile Code = "entry.secret_file"
)

// Kind is the type of an archive entry, mapped from the tar typeflag by the
// adapter so the core does not depend on archive/tar.
type Kind int

const (
	KindFile Kind = iota
	KindDir
	KindSymlink
	KindHardlink
	KindOther
)

// Limits bound one deployment. The per-entry ones are enforced by CheckEntry;
// the totals (entries, extracted bytes) by Extract, which counts real bytes.
type Limits struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxEntries    int
	MaxDepth      int
}

// DefaultLimits are the documented defaults.
var DefaultLimits = Limits{
	MaxFileBytes:  100 << 20,
	MaxTotalBytes: 500 << 20,
	MaxEntries:    20000,
	MaxDepth:      32,
}

// EntryDecision is the verdict on one archive entry.
type EntryDecision struct {
	Allowed bool
	// Path is the cleaned, slash-separated path relative to the site root,
	// set only when Allowed. "" means the root itself (a "." or "./" entry),
	// which the extractor skips.
	Path   string
	Code   Code
	Reason string
}

// CheckEntry decides whether one archive entry may be extracted.
//
// name is the entry name exactly as the archive stores it; tools often write
// "./index.html", so a leading "./" is accepted and removed. Directory
// entries may end in "/". The reasons are fixed strings: the entry name is
// attacker-controlled and is never echoed back.
func CheckEntry(name string, kind Kind, size int64, lim Limits) EntryDecision {
	switch kind {
	case KindFile, KindDir:
	case KindSymlink, KindHardlink:
		return refuse(Link, "links are not extracted")
	default:
		return refuse(Type, "only regular files and directories are extracted")
	}
	if size < 0 || (kind == KindFile && size > lim.MaxFileBytes) {
		return refuse(TooLarge, "entry is larger than the per-file limit")
	}

	// Refuse what is never a relative slash path before any cleaning, so a
	// normalisation step cannot turn a hostile name into a harmless-looking one.
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || hasDriveLetter(name) {
		return refuse(UnsafePath, "entry name is not a relative slash-separated path")
	}
	// ".." is refused anywhere, even where Clean would resolve it inside the
	// site: an archive that needs it was not produced by a build tool.
	for part := range strings.SplitSeq(name, "/") {
		if part == ".." {
			return refuse(UnsafePath, "entry name contains a parent-directory component")
		}
	}

	// Clean, not a manual TrimPrefix("./"): trimming ".//" leaves "/", an
	// absolute path that already passed the check above (found by the fuzzer,
	// kept in testdata). Clean of a relative path never becomes absolute.
	clean := path.Clean(name)
	if clean == "." {
		return EntryDecision{Allowed: true, Path: "", Code: EntryAllowed, Reason: "site root"}
	}
	if strings.Count(clean, "/")+1 > lim.MaxDepth {
		return refuse(TooDeep, "entry is nested deeper than the limit")
	}
	for part := range strings.SplitSeq(clean, "/") {
		if secretName(part) {
			return refuse(SecretFile, "entry name belongs to a secret or repository file")
		}
	}
	return EntryDecision{Allowed: true, Path: clean, Code: EntryAllowed, Reason: "regular entry inside the site"}
}

// secretName reports a path component that never belongs in a public site.
// Case is ignored: a case-insensitive filesystem would serve .ENV as .env.
// Contents are not scanned here; CI checks the build output for credential
// patterns before uploading it.
func secretName(part string) bool {
	part = strings.ToLower(part)
	switch {
	case part == ".env" || strings.HasPrefix(part, ".env."):
		return true
	case part == ".git" || part == ".npmrc":
		return true
	case strings.HasSuffix(part, ".pub"):
		return false
	}
	for _, key := range []string{"id_rsa", "id_ed25519", "id_ecdsa", "id_dsa"} {
		if strings.HasPrefix(part, key) {
			return true
		}
	}
	return false
}

func refuse(code Code, reason string) EntryDecision {
	return EntryDecision{Code: code, Reason: reason}
}

// hasDriveLetter reports a Windows volume prefix such as "C:": harmless on
// Unix, an absolute path when the server runs on Windows.
func hasDriveLetter(name string) bool {
	return len(name) >= 2 && name[1] == ':' && (name[0]|0x20 >= 'a' && name[0]|0x20 <= 'z')
}
