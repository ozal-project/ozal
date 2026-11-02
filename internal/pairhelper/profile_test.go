package pairhelper

import (
	"strings"
	"testing"
)

func validProfile() Profile {
	return Profile{Unit: "ozal-edge.service", ServiceUser: "ozal", ServiceGroup: "ozal", Binary: "/usr/local/lib/ozal/ozal", Config: "/etc/ozal/config.json", StateDB: "/var/lib/ozal/state.db", Stage: "/var/lib/ozal/stage", Socket: "/run/ozal/admin.sock", Peer: "portfolio", Keep: 3}
}

func TestProfileRejectsUnsafeValues(t *testing.T) {
	tests := map[string]func(*Profile){
		"relative binary":     func(p *Profile) { p.Binary = "bin/ozal" },
		"parent traversal":    func(p *Profile) { p.Stage = "/var/lib/ozal/../tmp" },
		"shell unit":          func(p *Profile) { p.Unit = "edge.service; id" },
		"stage in binary dir": func(p *Profile) { p.Stage = "/usr/local/lib/ozal/stage" },
		"invalid retention":   func(p *Profile) { p.Keep = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := validProfile()
			mutate(&p)
			if err := p.Validate(); err == nil {
				t.Fatal("Validate() accepted unsafe profile")
			}
		})
	}
}

func TestDecodeProfileRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	for name, input := range map[string]string{
		"unknown":  `{"unit":"edge.service","unexpected":true}`,
		"trailing": `{"unit":"edge.service"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeProfile(strings.NewReader(input)); err == nil {
				t.Fatal("DecodeProfile() accepted invalid document")
			}
		})
	}
}

func TestRequestActionAllowlist(t *testing.T) {
	valid := []Request{
		{Action: "preflight"}, {Action: "start"}, {Action: "stop"},
		{Action: "checkpoint", Job: "pair-20261102T173850Z-5399444b627e9f01", SHA256: strings.Repeat("b", 64)},
		{Action: "install", Job: "pair-20261102T173850Z-5399444b627e9f01", SHA256: strings.Repeat("a", 64), Ref: "v0.1.0-alpha.5"},
		{Action: "ready", Job: "pair-20261102T173850Z-5399444b627e9f01", SHA256: strings.Repeat("a", 64)},
		{Action: "restore", Job: "pair-20261102T173850Z-5399444b627e9f01"},
		{Action: "report", Progress: []byte(`{"phase":"complete","result":"updated"}`)},
	}
	for _, q := range valid {
		if err := q.Validate(); err != nil {
			t.Errorf("%s: %v", q.Action, err)
		}
	}
	for _, q := range []Request{
		{Action: "shell", Progress: []byte(`{"cmd":"id"}`)},
		{Action: "install", Job: "../../etc/passwd", SHA256: strings.Repeat("a", 64)},
		{Action: "install", Job: "pair-20261102T173850Z-5399444b627e9f01", SHA256: "bad", Ref: "v0.1.0-alpha.5"},
		{Action: "install", Job: "pair-20261102T173850Z-5399444b627e9f01", SHA256: strings.Repeat("a", 64), Ref: "../../bad"},
		{Action: "stop", Progress: []byte(`{}`)},
		{Action: "report", Progress: []byte(`{`)},
	} {
		if err := q.Validate(); err == nil {
			t.Errorf("Validate() accepted %#v", q)
		}
	}
}

func TestValidateUpgradeRequiresStrictlyNewerSignedRelease(t *testing.T) {
	oldHash, newHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	tests := []struct {
		name, installed, installedHash, candidate, candidateHash string
		wantErr                                                  bool
	}{
		{"newer", "v1.0.0", oldHash, "v1.1.0", newHash, false},
		{"downgrade", "v1.1.0", oldHash, "v1.0.0", newHash, true},
		{"same tag different bytes", "v1.1.0", oldHash, "v1.1.0", newHash, true},
		{"same release", "v1.1.0", oldHash, "v1.1.0", oldHash, true},
		{"unversioned installed", "dev", oldHash, "v1.1.0", newHash, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUpgrade(tt.installed, tt.installedHash, tt.candidate, tt.candidateHash)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateUpgrade() error = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}
