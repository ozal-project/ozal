package config

import (
	"strings"
	"testing"
)

func rateLimitConfig(t *testing.T, rateLimit string) *Config {
	t.Helper()
	path := writeConfig(t, `{
		"listen": ":8888",
		"rate_limit": `+rateLimit+`,
		"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestRateLimitAcceptsIdentityAndCapacity(t *testing.T) {
	cfg := rateLimitConfig(t, `{
		"general":  {"rate": 20, "burst": 40},
		"auth":     {"rate": 1, "burst": 3},
		"identity": {"rate": 10, "burst": 30},
		"max_keys": 5000
	}`)
	if cfg.RateLimit.Identity == nil || cfg.RateLimit.Identity.Rate != 10 {
		t.Fatalf("identity = %+v", cfg.RateLimit.Identity)
	}
	if cfg.RateLimit.MaxKeys != 5000 {
		t.Fatalf("max_keys = %d", cfg.RateLimit.MaxKeys)
	}
	clone := Clone(*cfg)
	clone.RateLimit.Identity.Rate = 99
	if cfg.RateLimit.Identity.Rate == 99 {
		t.Fatal("Clone shares the identity limit pointer")
	}
}

func TestRateLimitRefusals(t *testing.T) {
	tests := []struct {
		name      string
		rateLimit string
		want      string
	}{
		{"identity without rate", `{"general":{"rate":1,"burst":1},"auth":{"rate":1,"burst":1},"identity":{"rate":0,"burst":5}}`, "rate_limit.identity needs rate > 0"},
		{"identity without burst", `{"general":{"rate":1,"burst":1},"auth":{"rate":1,"burst":1},"identity":{"rate":1,"burst":0}}`, "rate_limit.identity needs rate > 0 and burst >= 1"},
		{"max_keys below floor", `{"general":{"rate":1,"burst":1},"auth":{"rate":1,"burst":1},"max_keys":10}`, "rate_limit.max_keys"},
		{"max_keys above ceiling", `{"general":{"rate":1,"burst":1},"auth":{"rate":1,"burst":1},"max_keys":2000000}`, "rate_limit.max_keys"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, `{
				"listen": ":8888",
				"rate_limit": `+tt.rateLimit+`,
				"routes": [{"host": "a.localhost", "target": "http://localhost:9000"}]
			}`)
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}
