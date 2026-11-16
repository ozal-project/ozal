package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/cloudflare"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
)

// Connecting Cloudflare from the console. The console takes an API token,
// checks it with Cloudflare, and keeps it in a 0600 file of its own beside
// the state database, never in config.json and never shown again. Turning
// certificates over to DNS-01 changes how the public listener gets them and
// needs a restart, which stays with the host: the console prints the one
// command that reviews and applies that change (ozal cloudflare enable).

const (
	cloudflareTokenFile = "cloudflare.token"
	cloudflareInfoFile  = "cloudflare.json"
)

// CloudflareSetup wires the console's Cloudflare connection.
type CloudflareSetup struct {
	// Dir holds the token and what was learnt when it was checked.
	Dir string
	// Verify checks a token with Cloudflare and returns the zones it sees.
	Verify func(ctx context.Context, token string) ([]string, error)
	// Command is the host command that enables certificates with the token.
	Command func(tokenFile, target string) string
	// Lookup resolves the sign-in host, to suggest this edge's address.
	Lookup func(ctx context.Context, host string) ([]string, error)
}

// WithCloudflareSetup lets the console connect Cloudflare.
func WithCloudflareSetup(setup CloudflareSetup) Option {
	return func(h *Handler) {
		if setup.Lookup == nil {
			setup.Lookup = net.DefaultResolver.LookupHost
		}
		h.cloudflare = &setup
	}
}

type cloudflareInfo struct {
	Zones      []string  `json:"zones"`
	Target     string    `json:"target"`
	VerifiedAt time.Time `json:"verified_at"`
}

func (h *Handler) registerCloudflare() {
	h.mux.HandleFunc("GET /_/api/cloudflare", h.requireTool("applications", h.cloudflareStatus))
	h.mux.HandleFunc("POST /_/api/cloudflare/token", h.requireTool("applications", h.saveCloudflareToken))
	h.mux.HandleFunc("DELETE /_/api/cloudflare/token", h.requireTool("applications", h.forgetCloudflareToken))
}

// cloudflareActive says the edge already obtains certificates by DNS-01
// through Cloudflare and can manage records and zones with it.
func cloudflareActive(cfg config.Config) bool {
	d := cfg.TLS.DNS01
	return d != nil && (d.DNSProvider == "" || d.DNSProvider == "cloudflare") && cfg.DNS != nil && cfg.DNS.Cloudflare != nil
}

// cloudflareTokenInUse also covers a host that uses the saved file for
// certificates or DNS records alone, without the full console setup.
func cloudflareTokenInUse(cfg config.Config, tokenRef string) bool {
	if cloudflareActive(cfg) {
		return true
	}
	if cfg.TLS.DNS01 != nil && cfg.TLS.DNS01.DNSToken == tokenRef {
		return true
	}
	return cfg.DNS != nil && cfg.DNS.Cloudflare != nil && cfg.DNS.Cloudflare.Token == tokenRef
}

func (h *Handler) cloudflareStatus(w http.ResponseWriter, r *http.Request) {
	h.cloudflareMu.Lock()
	defer h.cloudflareMu.Unlock()
	h.writeCloudflareStatus(w, r)
}

func (h *Handler) writeCloudflareStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	cfg := h.manager.Config()
	out := map[string]any{"available": h.cloudflare != nil, "active": cloudflareActive(cfg), "saved": false, "permissions": cloudflarePermissions}
	if h.cloudflare == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	tokenPath := filepath.Join(h.cloudflare.Dir, cloudflareTokenFile)
	if _, err := os.Stat(tokenPath); err == nil {
		out["saved"] = true
		var info cloudflareInfo
		if data, err := os.ReadFile(filepath.Join(h.cloudflare.Dir, cloudflareInfoFile)); err == nil && json.Unmarshal(data, &info) == nil {
			out["zones"], out["target"], out["verified_at"] = info.Zones, info.Target, info.VerifiedAt
			if !cloudflareActive(cfg) {
				// The same plan the host command will apply, to review here
				// first: which certificates, covering which addresses.
				plan, err := cloudflare.PlanEnable(cfg, cfg, "file:"+tokenPath, filepath.Join(h.cloudflare.Dir, "dns01"), info.Target, info.Zones)
				if err != nil {
					out["plan_error"] = err.Error()
				} else {
					out["plan"] = map[string]any{"zones": plan.Zones, "covers": plan.Covers, "local": nonNil(plan.Local)}
					if h.cloudflare.Command != nil {
						out["command"] = h.cloudflare.Command(tokenPath, info.Target)
					}
				}
			}
		}
	}
	if cfg.DNS != nil && cfg.DNS.Target != "" {
		out["suggested_target"] = cfg.DNS.Target
	} else if host := loginHostname(cfg); host != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		addrs, err := h.cloudflare.Lookup(ctx, host)
		cancel()
		if err == nil {
			if addr := publicAddress(addrs); addr != "" {
				out["suggested_target"] = addr
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// cloudflarePermissions are what the token needs, in Cloudflare's words.
var cloudflarePermissions = []string{"Zone · Zone · Read", "Zone · DNS · Edit", "Zone · Zone Settings · Edit", "Zone · SSL and Certificates · Edit"}

// A Cloudflare API token is 40 URL-safe characters today; allow some room.
var cloudflareTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,200}$`)

func (h *Handler) saveCloudflareToken(w http.ResponseWriter, r *http.Request) {
	if h.cloudflare == nil {
		writeError(w, http.StatusServiceUnavailable, "this edge has no state directory to keep a Cloudflare token in")
		return
	}
	if cloudflareTokenInUse(h.manager.Config(), "file:"+filepath.Join(h.cloudflare.Dir, cloudflareTokenFile)) {
		writeError(w, http.StatusConflict, "the edge uses this token for its certificates; change it in the host configuration")
		return
	}
	var req struct {
		Token  string `json:"token"`
		Target string `json:"target"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "send the token and this edge's public address")
		return
	}
	token := strings.TrimSpace(req.Token)
	if !cloudflareTokenPattern.MatchString(token) {
		writeError(w, http.StatusBadRequest, "that does not look like a Cloudflare API token: paste the token itself, not its name or ID")
		return
	}
	target, err := cleanTarget(req.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.recordMutationIntent(w, r, "cloudflare.token", "") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	zones, err := h.cloudflare.Verify(ctx, token)
	cancel()
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, cloudflareRefusal(err), "token_refused")
		return
	}
	h.cloudflareMu.Lock()
	defer h.cloudflareMu.Unlock()
	if cloudflareTokenInUse(h.manager.Config(), "file:"+filepath.Join(h.cloudflare.Dir, cloudflareTokenFile)) {
		writeError(w, http.StatusConflict, "the edge now uses its token; change it in the host configuration")
		return
	}
	info, _ := json.Marshal(cloudflareInfo{Zones: zones, Target: target, VerifiedAt: time.Now().UTC()})
	if err := writeSecretFile(filepath.Join(h.cloudflare.Dir, cloudflareTokenFile), []byte(token+"\n")); err != nil {
		writeError(w, http.StatusInternalServerError, "the token could not be stored")
		return
	}
	if err := writeSecretFile(filepath.Join(h.cloudflare.Dir, cloudflareInfoFile), info); err != nil {
		writeError(w, http.StatusInternalServerError, "the token's details could not be stored")
		return
	}
	h.emit(events.Event{Type: events.Type("cloudflare.token_saved"), Actor: h.actorOf(r), Reason: fmt.Sprintf("%d zone(s) visible", len(zones))})
	h.writeCloudflareStatus(w, r)
}

func (h *Handler) forgetCloudflareToken(w http.ResponseWriter, r *http.Request) {
	h.cloudflareMu.Lock()
	defer h.cloudflareMu.Unlock()
	if h.cloudflare == nil {
		writeError(w, http.StatusServiceUnavailable, "this edge keeps no Cloudflare token")
		return
	}
	if cloudflareTokenInUse(h.manager.Config(), "file:"+filepath.Join(h.cloudflare.Dir, cloudflareTokenFile)) {
		writeError(w, http.StatusConflict, "the edge uses this token for its certificates; change that in the host configuration first")
		return
	}
	if !h.recordMutationIntent(w, r, "cloudflare.token_forget", "") {
		return
	}
	for _, name := range []string{cloudflareTokenFile, cloudflareInfoFile} {
		if err := os.Remove(filepath.Join(h.cloudflare.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, "the token could not be removed")
			return
		}
	}
	h.emit(events.Event{Type: events.Type("cloudflare.token_removed"), Actor: h.actorOf(r)})
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// cloudflareRefusal says why Cloudflare refused a token, in words for the
// person pasting it. The error comes from Cloudflare's answer and never
// contains the token itself.
func cloudflareRefusal(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "Invalid API Token") || strings.Contains(text, "API error 401"):
		return "Cloudflare did not accept the token: it is not valid. Copy it again from the page Cloudflare showed when you created it; Cloudflare shows it only once."
	case strings.Contains(text, "not active"):
		return "Cloudflare did not accept the token: it is disabled or expired. Activate it, or create a new one."
	case strings.Contains(text, "sees no zone"):
		return "The token works but sees no zone. Edit it in Cloudflare so it includes the zones Ozal serves, with Zone · Read."
	case strings.Contains(text, "API error 403"):
		return "Cloudflare did not accept the token: it lacks a permission it needs (" + strings.Join(cloudflarePermissions, ", ") + ")."
	}
	return "Cloudflare could not be asked about the token: " + text
}

// writeSecretFile replaces a file atomically, readable only by this user.
func writeSecretFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ozal-secret-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// cleanTarget accepts a public IP address or a host name.
func cleanTarget(raw string) (string, error) {
	target := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if target == "" {
		return "", errors.New("the public address of this edge is needed: the IP address or host name visitors reach it at")
	}
	if addr, err := netip.ParseAddr(target); err == nil {
		if !addr.IsGlobalUnicast() || addr.IsPrivate() {
			return "", fmt.Errorf("%s is not a public address; visitors could not reach it", target)
		}
		return addr.String(), nil
	}
	if _, err := config.NormalizeZoneName(target); err != nil {
		return "", fmt.Errorf("%q is neither a public IP address nor a host name", raw)
	}
	return target, nil
}

func loginHostname(cfg config.Config) string {
	if cfg.Auth.Passkeys == nil {
		return ""
	}
	u, err := url.Parse(cfg.Auth.Passkeys.LoginURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// publicAddress picks the first public IPv4, else IPv6, address.
func publicAddress(addrs []string) string {
	var v6 []string
	for _, a := range addrs {
		addr, err := netip.ParseAddr(a)
		if err != nil || !addr.IsGlobalUnicast() || addr.IsPrivate() {
			continue
		}
		if addr.Is4() {
			return addr.String()
		}
		v6 = append(v6, addr.String())
	}
	if len(v6) > 0 {
		slices.Sort(v6)
		return v6[0]
	}
	return ""
}
