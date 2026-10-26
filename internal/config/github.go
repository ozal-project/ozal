package config

import "fmt"

// GitHubApp is optional repository discovery, separate from workflow OIDC.
// The signing key is always a reference and never returned by the admin API.
type GitHubApp struct {
	AppID          int64  `json:"app_id"`
	InstallationID int64  `json:"installation_id"`
	PrivateKey     string `json:"private_key"`
}

func (g *GitHubApp) Validate() error {
	if g == nil {
		return nil
	}
	if g.AppID < 1 || g.InstallationID < 1 {
		return fmt.Errorf("github app_id and installation_id must be positive")
	}
	if err := validateProjectTokenReference(g.PrivateKey); err != nil {
		return fmt.Errorf("github private_key must be an env:/file: reference")
	}
	return nil
}
