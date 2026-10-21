package passkey

import "testing"

func TestProviderName(t *testing.T) {
	icloud := []byte{0xfb, 0xfc, 0x30, 0x07, 0x15, 0x4e, 0x4e, 0xcc, 0x8c, 0x0b, 0x6e, 0x02, 0x05, 0x57, 0xd7, 0xbd}
	google := []byte{0xea, 0x9b, 0x8d, 0x66, 0x4d, 0x01, 0x1d, 0x21, 0x3c, 0xe4, 0xb6, 0xb4, 0x8c, 0xb5, 0x75, 0xd4}
	for name, tc := range map[string]struct {
		aaguid []byte
		synced bool
		want   string
		icon   string
	}{
		"Apple":                      {icloud, true, "Apple Passwords", "apple"},
		"Google":                     {google, true, "Google Password Manager", "google"},
		"unknown synced":             {make([]byte, 16), true, "Synced passkey", "synced"},
		"unknown device-bound":       {make([]byte, 16), false, "Security key or device passkey", "key"},
		"no AAGUID (no attestation)": {nil, false, "Security key or device passkey", "key"},
	} {
		got, icon := Provider(tc.aaguid, tc.synced)
		if got != tc.want || icon != tc.icon {
			t.Errorf("%s: %q %q, want %q %q", name, got, icon, tc.want, tc.icon)
		}
	}
}

// Every icon a provider names is bundled, or the page would show a broken image.
func TestProviderIconsAreBundled(t *testing.T) {
	icons := map[string]bool{"synced": true, "key": true}
	for _, p := range providers {
		icons[p.icon] = true
	}
	for icon := range icons {
		if _, err := assets.ReadFile("assets/icons/" + icon + ".svg"); err != nil {
			t.Errorf("icon %q: %v", icon, err)
		}
	}
}
