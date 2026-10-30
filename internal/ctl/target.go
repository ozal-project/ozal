// Package ctl is the operator-side toolkit behind cmd/ozalctl: it ships a
// Ozal binary to an edge and runs `ozal upgrade` there. It runs
// on the operator's machine and is never part of the edge's own binary. This
// file is the functional core: targets, validation, the commands sent to the
// edge and the reading of their results. Nothing here touches a network.
package ctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Target is one edge the operator ships to. Everything the edge's layout
// decides (paths, unit, account) is data, never a constant in the tool, so a
// second server is a second entry.
type Target struct {
	SSH       SSH        `json:"ssh"`
	Edge      Edge       `json:"edge"`
	Connector *Connector `json:"connector,omitempty"`
}

// SSH is how to reach the edge.
type SSH struct {
	Target string `json:"target"` // user@host
	Key    string `json:"key"`    // identity file; ~ is expanded by the adapter
}

// Edge describes the installed service on the host.
type Edge struct {
	Unit        string `json:"unit"`
	ServiceUser string `json:"service_user"`
	Binary      string `json:"binary"`
	Config      string `json:"config"`
	StateDB     string `json:"state_db"`
	// Stage is a directory the SSH user owns: uploads land here and are only
	// run by root after a hash check on a root-owned copy.
	Stage string `json:"stage"`
	// Lock is a directory path on the edge: one deployment at a time,
	// whichever tool runs it.
	Lock string `json:"lock"`
	// CommandLink, when set, is created (if absent) as a link to Binary so the
	// `ozal` command runs from any directory.
	CommandLink string `json:"command_link,omitempty"`
	Keep        int    `json:"keep,omitempty"`
	// Socket is the edge's admin Unix socket. `edge config` reads the
	// digest in effect there as root, with no token.
	Socket string `json:"socket,omitempty"`
	// SocketGroup says the SSH user belongs to the edge's admin.socket_group:
	// API-backed commands (app alias, project trust) then open Socket as that
	// user, with no sudo and no terminal. Leave it empty to keep `sudo -u`.
	SocketGroup string `json:"socket_group,omitempty"`
	// Admin is the edge's loopback admin listener (host:port on the edge);
	// `site publish` reaches it through an SSH tunnel.
	Admin string `json:"admin,omitempty"`
	// SitesToken is a file on the operator's machine holding the edge's
	// admin.sites_token, which can publish and roll back sites and nothing
	// else. It is read when needed and never printed or sent to the edge
	// on a command line.
	SitesToken string `json:"sites_token,omitempty"`
}

// Connector describes the user-owned systemd service on the tunnel peer. Its
// SSH login is the service account; no root command is needed on that host.
type Connector struct {
	SSH         SSH    `json:"ssh"`
	Name        string `json:"name"`
	Unit        string `json:"unit"`
	ServiceUser string `json:"service_user"`
	Binary      string `json:"binary"`
	Stage       string `json:"stage"`
	Lock        string `json:"lock"`
}

var (
	unitName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,63}$`)
	account  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	sshUser  = regexp.MustCompile(`^[A-Za-z0-9_.-]+@[A-Za-z0-9._:-]+$`)
)

// Validate rejects anything that is not a plain absolute path or a plain
// name: the values end up in shell commands on the edge, which are quoted too,
// but a target file is not an input worth being clever about.
func (t Target) Validate() error {
	if !sshUser.MatchString(t.SSH.Target) {
		return errors.New("ssh.target must look like user@host")
	}
	if strings.TrimSpace(t.SSH.Key) == "" {
		return errors.New("ssh.key is required")
	}
	if !unitName.MatchString(t.Edge.Unit) {
		return errors.New("edge.unit must be a plain systemd unit name")
	}
	if !account.MatchString(t.Edge.ServiceUser) {
		return errors.New("edge.service_user must be a plain account name")
	}
	for name, p := range map[string]string{
		"edge.binary": t.Edge.Binary, "edge.config": t.Edge.Config, "edge.state_db": t.Edge.StateDB,
		"edge.stage": t.Edge.Stage, "edge.lock": t.Edge.Lock,
	} {
		if err := plainAbsolute(p); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if t.Edge.CommandLink != "" {
		if err := plainAbsolute(t.Edge.CommandLink); err != nil {
			return fmt.Errorf("edge.command_link: %w", err)
		}
	}
	if t.Edge.Socket != "" {
		if err := plainAbsolute(t.Edge.Socket); err != nil {
			return fmt.Errorf("edge.socket: %w", err)
		}
	}
	if t.Edge.SocketGroup != "" {
		if t.Edge.Socket == "" || !account.MatchString(t.Edge.SocketGroup) {
			return errors.New("edge.socket_group needs edge.socket and a plain group name")
		}
	}
	if t.Edge.Admin != "" {
		host, port, err := net.SplitHostPort(t.Edge.Admin)
		if addr, perr := netip.ParseAddr(host); err != nil || perr != nil || !addr.IsLoopback() || port == "" {
			return errors.New("edge.admin must be a loopback host:port on the edge, such as 127.0.0.1:9091")
		}
	}
	if t.Edge.Keep < 0 || t.Edge.Keep > 20 {
		return errors.New("edge.keep must be between 0 and 20")
	}
	if t.Connector != nil {
		c := t.Connector
		if !sshUser.MatchString(c.SSH.Target) || strings.TrimSpace(c.SSH.Key) == "" {
			return errors.New("connector ssh.target and ssh.key are required")
		}
		connectorSSHUser, _, _ := strings.Cut(c.SSH.Target, "@")
		if !account.MatchString(c.ServiceUser) || connectorSSHUser != c.ServiceUser {
			return errors.New("connector ssh user must equal connector.service_user")
		}
		if !unitName.MatchString(c.Unit) || !unitName.MatchString(c.Name) {
			return errors.New("connector name and unit must be plain names")
		}
		for field, pathValue := range map[string]string{
			"connector.binary": c.Binary, "connector.stage": c.Stage, "connector.lock": c.Lock,
		} {
			if err := plainAbsolute(pathValue); err != nil {
				return fmt.Errorf("%s: %w", field, err)
			}
		}
		if c.Stage == c.Binary || c.Lock == c.Binary || c.Lock == c.Stage {
			return errors.New("connector binary, stage and lock paths must be distinct")
		}
		if t.Edge.Socket == "" {
			return errors.New("edge.socket is required for coordinated updates")
		}
		if t.Edge.StateDB == "" {
			return errors.New("edge.state_db is required for coordinated rollback")
		}
	}
	return nil
}

func plainAbsolute(p string) error {
	if !path.IsAbs(p) || path.Clean(p) != p {
		return errors.New("must be a clean absolute path")
	}
	if strings.ContainsAny(p, "\x00\n\r") {
		return errors.New("must not contain control characters")
	}
	return nil
}

// Targets is the file operators keep per project.
type Targets map[string]Target

// ParseTargets reads {"targets": {"name": {...}}} strictly: an unknown field
// is a typo, not something to ignore.
func ParseTargets(r io.Reader) (Targets, error) {
	var file struct {
		Targets Targets `json:"targets"`
	}
	decoder := json.NewDecoder(io.LimitReader(r, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("targets file: %w", err)
	}
	if len(file.Targets) == 0 {
		return nil, errors.New("targets file: no targets")
	}
	for name, target := range file.Targets {
		if err := target.Validate(); err != nil {
			return nil, fmt.Errorf("target %q: %w", name, err)
		}
	}
	return file.Targets, nil
}

// Pick returns the named target, or the only one when no name is given.
func (t Targets) Pick(name string) (string, Target, error) {
	if name == "" {
		if len(t) == 1 {
			for n, target := range t {
				return n, target, nil
			}
		}
		return "", Target{}, fmt.Errorf("name a target with -target (%s)", strings.Join(t.names(), ", "))
	}
	target, ok := t[name]
	if !ok {
		return "", Target{}, fmt.Errorf("no target %q (%s)", name, strings.Join(t.names(), ", "))
	}
	return name, target, nil
}

func (t Targets) names() []string {
	names := make([]string, 0, len(t))
	for name := range t {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
