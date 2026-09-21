// Package config loads Switchboard's routing table from a JSON file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Route struct {
	Host       string `json:"host"`
	Target     string `json:"target"`
	HealthPath string `json:"health_path,omitempty"` // defaults to "/" if empty
}

type TLS struct {
	Enabled  bool     `json:"enabled"`
	Domains  []string `json:"domains,omitempty"`
	CacheDir string   `json:"cache_dir,omitempty"`
	Email    string   `json:"email,omitempty"`
}

type Config struct {
	Listen string  `json:"listen"`
	TLS    TLS     `json:"tls,omitempty"`
	Routes []Route `json:"routes"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Listen == "" {
		return nil, fmt.Errorf("config: listen address is required")
	}
	if len(cfg.Routes) == 0 {
		return nil, fmt.Errorf("config: at least one route is required")
	}
	for _, r := range cfg.Routes {
		if r.Host == "" || r.Target == "" {
			return nil, fmt.Errorf("config: route missing host or target: %+v", r)
		}
	}
	if cfg.TLS.Enabled {
		if len(cfg.TLS.Domains) == 0 {
			return nil, fmt.Errorf("config: tls.enabled requires at least one domain")
		}
		if cfg.TLS.CacheDir == "" {
			return nil, fmt.Errorf("config: tls.enabled requires cache_dir")
		}
	}

	return &cfg, nil
}
