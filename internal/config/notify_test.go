package config

import (
	"strings"
	"testing"
)

func notifyConfig(t *testing.T, notifyJSON string) *Config {
	t.Helper()
	path := writeConfig(t, `{
		"listen": ":8888",
		"notify": `+notifyJSON+`,
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestNotifyConfigAcceptsLocalBotAPI(t *testing.T) {
	cfg := notifyConfig(t, `{
		"telegram": {
			"token": "env:TELEGRAM_BOT_TOKEN",
			"chats_file": "/var/lib/switchboard/telegram-chats.json",
			"api_base": "http://127.0.0.1:8081"
		}
	}`)
	if cfg.Notify == nil || cfg.Notify.Telegram == nil {
		t.Fatal("notify.telegram was dropped")
	}
	if cfg.Notify.Telegram.Token != "env:TELEGRAM_BOT_TOKEN" {
		t.Fatalf("token = %q", cfg.Notify.Telegram.Token)
	}
	clone := Clone(*cfg)
	clone.Notify.Telegram.ChatsFile = "/elsewhere.json"
	if cfg.Notify.Telegram.ChatsFile == "/elsewhere.json" {
		t.Fatal("Clone shares the telegram settings pointer")
	}
}

func TestNotifyConfigRefusals(t *testing.T) {
	tests := []struct {
		name   string
		notify string
		want   string
	}{
		{"no channel", `{}`, "notify requires telegram"},
		{"no token", `{"telegram":{"chats_file":"/var/lib/switchboard/chats.json"}}`, "requires token"},
		{"empty token reference", `{"telegram":{"token":"env: ","chats_file":"/var/lib/switchboard/chats.json"}}`, "names no environment variable"},
		{"no chats file", `{"telegram":{"token":"env:TOKEN"}}`, "requires chats_file"},
		{"relative chats file", `{"telegram":{"token":"env:TOKEN","chats_file":"chats.json"}}`, "absolute path"},
		{"plain http api base", `{"telegram":{"token":"env:TOKEN","chats_file":"/var/lib/switchboard/chats.json","api_base":"http://api.example.com"}}`, "must be https"},
		{"unparseable api base", `{"telegram":{"token":"env:TOKEN","chats_file":"/var/lib/switchboard/chats.json","api_base":"://"}}`, "is not a URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, `{
				"listen": ":8888",
				"notify": `+tt.notify+`,
				"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
			}`)
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}
