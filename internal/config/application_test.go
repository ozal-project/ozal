package config

import "testing"

func TestApplicationDomainAssociationAndAtomicValidation(t *testing.T) {
	cfg := Config{Listen: "127.0.0.1:8888", Pages: &Pages{Dir: t.TempDir()}, Routes: []Route{{Host: "one.example.com", Site: "blog"}}, Projects: []Project{{Name: "blog", Site: "blog", Hostnames: []string{"one.example.com"}, Deployers: []ProjectDeployer{{Token: "env:DEPLOY_TOKEN"}}}}}
	got, err := WithApplication(cfg, Route{Host: "TWO.example.com", Site: "blog"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects[0].Hostnames) != 2 || len(cfg.Projects[0].Hostnames) != 1 || len(cfg.Routes) != 1 {
		t.Fatal("project not updated or source mutated")
	}
	if _, err := WithApplication(cfg, Route{Host: "one.example.com", Target: "http://127.0.0.1:3000"}); err == nil {
		t.Fatal("orphaned a project's last hostname")
	}
}
