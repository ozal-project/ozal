package config

import (
	"strings"
	"testing"
)

func renameConfig(t *testing.T) Config {
	t.Helper()
	return Config{Listen: "127.0.0.1:8888", Pages: &Pages{Dir: t.TempDir()},
		Routes: []Route{
			{Host: "old.example.com", Site: "switchboard-site"},
			{Host: "new.example.com", Site: "switchboard-site"},
			{Host: "blog.example.com", Site: "blog"},
		},
		Projects: []Project{
			{Name: "switchboard-site", Site: "switchboard-site", Hostnames: []string{"old.example.com", "new.example.com"}, Deployers: []ProjectDeployer{{Token: "env:A"}}},
			{Name: "blog", Site: "blog", Hostnames: []string{"blog.example.com"}, Deployers: []ProjectDeployer{{Token: "env:B"}}},
		}}
}

// A rename moves the project, its site and every route of that site; it
// leaves other projects, hostnames and publisher trust alone.
func TestPlanProjectRenameMovesProjectSiteAndRoutes(t *testing.T) {
	cfg := renameConfig(t)
	next, project, hosts, err := PlanProjectRename(cfg, "switchboard-site", "ozal-site")
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "ozal-site" || project.Site != "ozal-site" || len(project.Hostnames) != 2 || project.Deployers[0].Token != "env:A" {
		t.Fatalf("project = %+v", project)
	}
	if strings.Join(hosts, ",") != "old.example.com,new.example.com" {
		t.Fatalf("hosts = %v", hosts)
	}
	for _, r := range next.Routes {
		if r.Host != "blog.example.com" && r.Site != "ozal-site" {
			t.Fatalf("route %s still serves %s", r.Host, r.Site)
		}
	}
	if next.Routes[2].Site != "blog" || next.Projects[1].Name != "blog" {
		t.Fatal("another project changed")
	}
	if cfg.Projects[0].Name != "switchboard-site" || cfg.Routes[0].Site != "switchboard-site" {
		t.Fatal("the input configuration was changed")
	}
}

func TestPlanProjectRenameRefusesNamesInUse(t *testing.T) {
	cfg := renameConfig(t)
	for _, c := range []struct{ from, to, want string }{
		{"switchboard-site", "blog", "already used"},
		{"switchboard-site", "switchboard-site", "already called"},
		{"missing", "x", "not found"},
		{"switchboard-site", "Not A Name", "lowercase"},
	} {
		if _, _, _, err := PlanProjectRename(cfg, c.from, c.to); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s -> %s: %v, want %q", c.from, c.to, err, c.want)
		}
	}
	cfg.Routes = append(cfg.Routes, Route{Host: "other.example.com", Site: "orphan"})
	if _, _, _, err := PlanProjectRename(cfg, "switchboard-site", "orphan"); err == nil || !strings.Contains(err.Error(), "already served") {
		t.Fatalf("a site another route serves was taken: %v", err)
	}
}
