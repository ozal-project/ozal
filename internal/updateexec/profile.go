// Package updateexec is the local installation executor. It runs apart from
// the web service, owns one job at a time, and accepts only the fixed
// operations in a root-owned profile: check the signed catalog, start the
// reviewed update, and read the durable job. It never accepts a path, a
// command, a unit, a shell argument or an arbitrary URL from a client.
package updateexec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxProfileBytes = 64 << 10

var (
	socketGroupText = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	accountText     = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	channelText     = regexp.MustCompile(`^(stable|preview)$`)
)

// Profile is the root-owned installation profile. Every value the executor
// passes to the updater comes from here; the console cannot choose any.
type Profile struct {
	Socket      string `json:"socket"`
	SocketGroup string `json:"socket_group,omitempty"`
	Unit        string `json:"unit"`
	Binary      string `json:"binary"`
	Config      string `json:"config"`
	DB          string `json:"db,omitempty"`
	User        string `json:"user,omitempty"`
	PublicKey   string `json:"public_key"`
	CatalogBase string `json:"catalog_base"`
	Channel     string `json:"channel"`
	Status      string `json:"status"`
	State       string `json:"state"`
	Jobs        string `json:"jobs"`
	// Lock optionally names the shared update lock. Empty uses the updater's
	// default beside the installed binary, which is root-owned.
	Lock string `json:"lock,omitempty"`
	Keep int    `json:"keep,omitempty"`
}

// LoadProfile reads and validates the profile. The file must not be writable
// by the group or other accounts; the installer makes it root-only. Ownership
// itself is checked by the systemd unit's User/Group contract, not by file
// mode alone, because tests do not run as root.
func LoadProfile(path string) (Profile, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Profile{}, fmt.Errorf("read the update executor profile: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxProfileBytes {
		return Profile{}, errors.New("the update executor profile is not a small regular file")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return Profile{}, errors.New("the update executor profile is writable by group or other accounts")
	}
	if os.Geteuid() == 0 && !ownedByRoot(info) {
		return Profile{}, errors.New("the update executor profile must be owned by root")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return Profile{}, errors.New("the update executor profile directory must be a real directory")
	}
	if parent.Mode().Perm()&0o022 != 0 {
		return Profile{}, errors.New("the update executor profile directory is writable by group or other accounts")
	}
	if os.Geteuid() == 0 && !ownedByRoot(parent) {
		return Profile{}, errors.New("the update executor profile directory must be owned by root")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, fmt.Errorf("read the update executor profile: %w", err)
	}
	var p Profile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return Profile{}, fmt.Errorf("update executor profile: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return Profile{}, errors.New("update executor profile: more than one JSON document")
	}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// Validate refuses an incomplete or inconsistent profile before the socket is
// created. A partially configured executor fails closed.
func (p Profile) Validate() error {
	for field, value := range map[string]string{
		"socket": p.Socket, "unit": p.Unit, "binary": p.Binary, "config": p.Config,
		"public_key": p.PublicKey, "catalog_base": p.CatalogBase, "channel": p.Channel,
		"status": p.Status, "state": p.State, "jobs": p.Jobs,
	} {
		if value == "" {
			return fmt.Errorf("update executor profile: %s is required", field)
		}
	}
	for field, value := range map[string]string{
		"socket": p.Socket, "binary": p.Binary, "config": p.Config, "public_key": p.PublicKey,
		"status": p.Status, "state": p.State, "jobs": p.Jobs,
	} {
		if !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return fmt.Errorf("update executor profile: %s must be an absolute clean path", field)
		}
	}
	if p.DB != "" {
		if !filepath.IsAbs(p.DB) || filepath.Clean(p.DB) != p.DB {
			return errors.New("update executor profile: db must be an absolute clean path")
		}
		if !accountText.MatchString(p.User) {
			return errors.New("update executor profile: a db requires the service account in user")
		}
	} else if p.User != "" {
		return errors.New("update executor profile: user has no db to own")
	}
	if p.SocketGroup != "" && !socketGroupText.MatchString(p.SocketGroup) {
		return errors.New("update executor profile: socket_group is invalid")
	}
	if p.Lock != "" && (!filepath.IsAbs(p.Lock) || filepath.Clean(p.Lock) != p.Lock) {
		return errors.New("update executor profile: lock must be an absolute clean path")
	}
	if !channelText.MatchString(p.Channel) {
		return errors.New("update executor profile: channel must be stable or preview")
	}
	if p.Keep < 0 || p.Keep > 20 {
		return errors.New("update executor profile: keep must be between 0 and 20")
	}
	if err := validateCatalogBase(p.CatalogBase); err != nil {
		return err
	}
	key, err := os.Lstat(p.PublicKey)
	if err != nil || !key.Mode().IsRegular() {
		return errors.New("update executor profile: public_key must be a regular file")
	}
	if key.Mode().Perm()&0o022 != 0 {
		return errors.New("update executor profile: public_key is writable by group or other accounts")
	}
	if os.Geteuid() == 0 && !ownedByRoot(key) {
		return errors.New("update executor profile: public_key must be owned by root")
	}
	// The executor runs this binary as root. A service account that can write
	// the file or its directory could turn an Ozal compromise into root.
	binary, err := os.Lstat(p.Binary)
	if err != nil || !binary.Mode().IsRegular() {
		return errors.New("update executor profile: binary must be a regular file")
	}
	if binary.Mode().Perm()&0o111 == 0 {
		return errors.New("update executor profile: binary is not executable")
	}
	if binary.Mode().Perm()&0o022 != 0 {
		return errors.New("update executor profile: binary is writable by group or other accounts")
	}
	if os.Geteuid() == 0 && !ownedByRoot(binary) {
		return errors.New("update executor profile: binary must be owned by root")
	}
	dir, err := os.Lstat(filepath.Dir(p.Binary))
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 {
		return errors.New("update executor profile: the binary directory must be a real directory")
	}
	if dir.Mode().Perm()&0o022 != 0 {
		return errors.New("update executor profile: the binary directory is writable by group or other accounts")
	}
	if os.Geteuid() == 0 && !ownedByRoot(dir) {
		return errors.New("update executor profile: the binary directory must be owned by root")
	}
	return nil
}

// CatalogURL is the fixed per-platform catalog address for this profile.
func (p Profile) CatalogURL(goos, goarch string) string {
	return strings.TrimSuffix(p.CatalogBase, "/") + "/" + p.Channel + "/" + goos + "/" + goarch
}

func validateCatalogBase(raw string) error {
	u, err := parseProfileURL(raw)
	if err != nil || u == nil {
		return errors.New("update executor profile: catalog_base must be an absolute HTTPS URL without credentials, query or fragment")
	}
	if u.Scheme != "https" && !loopbackHTTP(u) {
		return errors.New("update executor profile: catalog_base must be HTTPS (loopback HTTP is only for tests)")
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("update executor profile: catalog_base must be an absolute HTTPS URL without credentials, query or fragment")
	}
	return nil
}

func parseProfileURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("not an absolute URL")
	}
	return u, nil
}

func loopbackHTTP(u *url.URL) bool {
	if u == nil || u.Scheme != "http" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}
