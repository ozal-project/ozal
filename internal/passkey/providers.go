package passkey

import "fmt"

// providers maps the AAGUID an authenticator reports when a passkey is
// created to a name a person recognises and the icon shown beside it. The
// list covers the common passkey providers whose AAGUIDs are published by
// their vendors (as collected by the community passkey-authenticator-aaguids
// list); anything else falls back to what the backup flag says. A name is a
// label for the owner's own list, never a trust decision: without
// attestation the authenticator chooses what it reports.
//
// Brand icons are simple-icons v15.0.0 (CC0, assets/icons/LICENSE.md);
// "synced" and "key" are drawn for the fallbacks and for providers that have
// no icon there.
var providers = map[string]struct{ name, icon string }{
	"fbfc3007-154e-4ecc-8c0b-6e020557d7bd": {"Apple Passwords", "apple"},
	"dd4ec289-e01d-41c9-bb89-70fa845d4bf2": {"Apple Passwords (managed)", "apple"},
	"ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4": {"Google Password Manager", "google"},
	"adce0002-35bc-c60a-648b-0b25f1f05503": {"Chrome on Mac", "chrome"},
	"08987058-cadc-4b81-b6e1-30de50dcbe96": {"Windows Hello", "key"},
	"9ddd1817-af5a-4672-a2b9-3e3dd95000a9": {"Windows Hello", "key"},
	"6028b017-b1d4-4c02-b4b3-afcdafc96bb2": {"Windows Hello", "key"},
	"bada5566-a7aa-401f-bd96-45619a55120d": {"1Password", "1password"},
	"d548826e-79b4-db40-a3d8-11116f7e8349": {"Bitwarden", "bitwarden"},
	"531126d6-e717-415c-9320-3d9aa6981239": {"Dashlane", "dashlane"},
	"53414d53-554e-4700-0000-000000000000": {"Samsung Pass", "samsung"},
	"50726f74-6f6e-5061-7373-50726f746f6e": {"Proton Pass", "synced"},
	"fdb141b2-5d84-443e-8a35-4698c205a502": {"KeePassXC", "keepassxc"},
}

// Provider names a passkey for its owner and picks its icon: the provider
// when its AAGUID is known, otherwise what the backup flag says about it.
func Provider(aaguid []byte, synced bool) (name, icon string) {
	if len(aaguid) == 16 {
		id := fmt.Sprintf("%x-%x-%x-%x-%x", aaguid[0:4], aaguid[4:6], aaguid[6:8], aaguid[8:10], aaguid[10:16])
		if p, ok := providers[id]; ok {
			return p.name, p.icon
		}
	}
	if synced {
		return "Synced passkey", "synced"
	}
	return "Security key or device passkey", "key"
}

// ProviderName is Provider without the icon.
func ProviderName(aaguid []byte, synced bool) string {
	name, _ := Provider(aaguid, synced)
	return name
}
