package passkey

import (
	"fmt"
	"net/url"

	"github.com/octaviosantacruz/switchboard/internal/auth"
	"github.com/octaviosantacruz/switchboard/internal/config"
)

// InviteLink builds the enrolment link for an invitation token: the login
// host's enrolment page, an optional destination and the token in the
// fragment (never sent to a server or a link preview). returnTo must be an
// http(s) URL on a protected host of cfg or the login host, the same rule the
// gate applies; a refused destination is an error here, not a silent drop at
// enrolment. The CLI and the admin console share it.
func InviteLink(cfg config.Config, token, returnTo string) (string, error) {
	if cfg.Auth.Passkeys == nil {
		return "", fmt.Errorf("auth.passkeys is not configured")
	}
	query := ""
	if returnTo != "" {
		allowed := map[string]bool{}
		for _, r := range cfg.Routes {
			if r.RequiresAuth() {
				if host, err := config.NormalizeHost(r.Host); err == nil {
					allowed[host] = true
				}
			}
		}
		if login, err := url.Parse(cfg.Auth.Passkeys.LoginURL); err == nil {
			if host, err := config.NormalizeHost(login.Host); err == nil {
				allowed[host] = true
			}
		}
		if auth.SafeReturnTo(allowed, returnTo) != returnTo {
			return "", fmt.Errorf("return_to %q is not an http(s) URL on a protected host of this config", returnTo)
		}
		query = "?return_to=" + url.QueryEscape(returnTo)
	}
	return cfg.Auth.Passkeys.LoginURL + "/_/auth/enrol" + query + "#" + token, nil
}
