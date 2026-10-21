package config

// AccessPolicy narrows who may reach a route beyond having a valid session.
//
// Semantics (enforced by auth.Authorize, not here):
//   - Route.Access == nil: the route is public unless legacy
//     Route.AuthRequired is true.
//   - Route.Access == &AccessPolicy{}: any authenticated user may pass.
//   - AllowedEmails / AllowedDomains: OR'd together — either allowlist
//     matching is enough. If both are empty, identity is not restricted.
//   - RequiredGroups: AND'd with the identity check — the principal must
//     carry every listed group in addition to passing the identity check.
//   - MaxAuthAgeMinutes: step-up. An allowed principal whose last login is
//     older is sent to log in again (auth.Fresh); zero means no limit. It
//     needs auth.passkeys: an upstream provider's single sign-on would
//     answer without a fresh proof.
//   - Methods: if set, only a session established with one of these login
//     methods ("passkey", "oidc") passes; others are sent to log in again.
//     Each must be configured in auth.
//   - RequireDeviceBound: only a session whose passkey cannot be synced (a
//     security key, a device-bound platform key) passes; others are sent to
//     log in with such a passkey. Also needs auth.passkeys.
type AccessPolicy struct {
	AllowedEmails      []string `json:"allowed_emails,omitempty"`
	AllowedDomains     []string `json:"allowed_domains,omitempty"`
	RequiredGroups     []string `json:"required_groups,omitempty"`
	MaxAuthAgeMinutes  int      `json:"max_auth_age_minutes,omitempty"`
	RequireDeviceBound bool     `json:"require_device_bound,omitempty"`
	Methods            []string `json:"methods,omitempty"`
}
