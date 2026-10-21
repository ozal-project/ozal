package passkey

import "testing"

func TestProviderName(t *testing.T) {
	icloud := []byte{0xfb, 0xfc, 0x30, 0x07, 0x15, 0x4e, 0x4e, 0xcc, 0x8c, 0x0b, 0x6e, 0x02, 0x05, 0x57, 0xd7, 0xbd}
	google := []byte{0xea, 0x9b, 0x8d, 0x66, 0x4d, 0x01, 0x1d, 0x21, 0x3c, 0xe4, 0xb6, 0xb4, 0x8c, 0xb5, 0x75, 0xd4}
	for name, tc := range map[string]struct {
		aaguid []byte
		synced bool
		want   string
	}{
		"iCloud":                     {icloud, true, "iCloud Keychain"},
		"Google":                     {google, true, "Google Password Manager"},
		"unknown synced":             {make([]byte, 16), true, "Synced passkey"},
		"unknown device-bound":       {make([]byte, 16), false, "Security key or device passkey"},
		"no AAGUID (no attestation)": {nil, false, "Security key or device passkey"},
	} {
		if got := ProviderName(tc.aaguid, tc.synced); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
