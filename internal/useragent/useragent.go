// Package useragent turns a browser's User-Agent into a short name a person
// recognises, for labelling their passkeys and sessions.
package useragent

import "strings"

// Label names a browser from its User-Agent:
// "Firefox on Linux", "Chrome on Android". It is a hint for the person who
// owns a passkey or a session, never an authorization input.
func Label(userAgent string) string {
	browser := ""
	for _, b := range []struct{ token, name string }{
		{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"FxiOS/", "Firefox"},
		{"CriOS/", "Chrome"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"},
	} {
		if strings.Contains(userAgent, b.token) {
			browser = b.name
			break
		}
	}
	system := ""
	for _, o := range []struct{ token, name string }{
		{"Android", "Android"}, {"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Windows", "Windows"},
		{"Mac OS X", "macOS"}, {"CrOS", "ChromeOS"}, {"Linux", "Linux"},
	} {
		if strings.Contains(userAgent, o.token) {
			system = o.name
			break
		}
	}
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	default:
		return system
	}
}
