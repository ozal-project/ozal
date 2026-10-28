package routemanager

import (
	"context"
	"github.com/switchboard-project/switchboard/internal/config"
	"testing"
	"time"
)

func TestReloadRejectsProcessOwnedAdapterChanges(t *testing.T) {
	for _, mode := range []string{"dns01", "notify"} {
		t.Run(mode, func(t *testing.T) {
			cfg := routeConfig("http://127.0.0.1:9000")
			if mode == "dns01" {
				cfg.TLS = config.TLS{Enabled: true, Domains: []string{"app.localhost"}, Email: "ops@example.com", DNS01: &config.DNS01{StateDir: t.TempDir(), DirectoryURL: "https://acme.example/directory", DNSToken: "env:SWITCHBOARD_TEST_TOKEN"}}
			}
			path := writeConfig(t, cfg)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			manager, err := New(ctx, path, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			installed := manager.current.Load()
			if mode == "dns01" {
				cfg.TLS.DNS01.DirectoryURL = "https://other.example/directory"
			} else {
				cfg.Notify = &config.Notify{Telegram: &config.Telegram{Token: "env:SWITCHBOARD_TEST_TOKEN", ChatsFile: t.TempDir() + "/chats.json"}}
			}
			overwriteConfig(t, path, cfg)
			if err := manager.Reload(); err == nil {
				t.Fatal("adapter change accepted without restart")
			}
			if manager.current.Load() != installed {
				t.Fatal("failed reload replaced installed snapshot")
			}
		})
	}
}
