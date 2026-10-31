package config

import "fmt"

// GitHubApp is optional repository discovery, separate from workflow OIDC.
// The signing key is always a reference and never returned by the admin API.
// An App created from the console exists before it is installed anywhere:
// InstallationID stays 0 until the operator chooses its repositories.
type GitHubApp struct {
	AppID          int64  `json:"app_id"`
	InstallationID int64  `json:"installation_id"`
	PrivateKey     string `json:"private_key"`
}

func (g *GitHubApp) Validate() error {
	if g == nil {
		return nil
	}
	if g.AppID < 1 || g.InstallationID < 0 {
		return fmt.Errorf("github app_id must be positive and installation_id must not be negative")
	}
	if err := validateProjectTokenReference(g.PrivateKey); err != nil {
		return fmt.Errorf("github private_key must be an env:/file: reference")
	}
	return nil
}

// Installed reports whether repositories can be listed and written.
func (g *GitHubApp) Installed() bool { return g != nil && g.InstallationID > 0 }
