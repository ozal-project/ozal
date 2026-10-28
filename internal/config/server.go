package config

import (
	"reflect"
	"slices"
)

// SameServer reports whether two configurations share everything a reload
// cannot change: listeners, TLS, the runtime file, notifications, audit,
// tunnel and Pages. When it is false the change needs a restart; when it is
// true a SIGHUP applies it (routes, auth, projects, trusted proxies, DNS).
func SameServer(a, b Config) bool {
	return a.RuntimeFile == b.RuntimeFile && a.Listen == b.Listen &&
		a.Admin.Listen == b.Admin.Listen &&
		a.TLS.Enabled == b.TLS.Enabled &&
		a.TLS.CacheDir == b.TLS.CacheDir &&
		a.TLS.Email == b.TLS.Email &&
		a.TLS.OriginClientCAFile == b.TLS.OriginClientCAFile &&
		reflect.DeepEqual(a.TLS.DNS01, b.TLS.DNS01) &&
		reflect.DeepEqual(a.Notify, b.Notify) &&
		reflect.DeepEqual(a.Audit, b.Audit) &&
		reflect.DeepEqual(a.Tunnel, b.Tunnel) &&
		reflect.DeepEqual(a.Pages, b.Pages) &&
		slices.Equal(a.TLS.Domains, b.TLS.Domains)
}
