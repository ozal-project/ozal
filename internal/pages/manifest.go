package pages

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// File is one extracted file of a deployment.
type File struct {
	Path   string // slash-separated, relative to the site root
	SHA256 string // hex
	Size   int64
}

// Manifest lists a deployment's files.
type Manifest struct {
	Files []File
	Bytes int64
}

// ManifestHash is the deployment ID: the SHA-256 of the sorted lines
// "path NUL sha256 LF". Directory entries, tar order and timestamps do not
// change it, so the same build always gets the same ID and a repeated upload
// reuses its directory. The encoding is unambiguous because CheckEntry
// refuses NUL in paths: each line has exactly one NUL, before a fixed-length hash.
func ManifestHash(files []File) string {
	lines := make([]string, len(files))
	for i, f := range files {
		lines[i] = f.Path + "\x00" + f.SHA256 + "\n"
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "")))
	return hex.EncodeToString(sum[:])
}
