package config

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestValidateProjects(t *testing.T) {
	portfolio := func(name, site, host, ref string, repositoryID int64) Project {
		return Project{
			Name: name, Site: site, Hostnames: []string{host},
			Deployers: []ProjectDeployer{{GitHub: &ProjectGitHubTrust{
				RepositoryID: repositoryID, RepositoryOwnerID: 42, Ref: ref,
			}}},
		}
	}
	projects := func(p ...Project) []Project { return p }
	routes := func(r ...Route) []Route { return r }

	tests := []struct {
		name               string
		projects           []Project
		routes             []Route
		wantErr            bool
		forbiddenErrorText string
	}{
		{
			name:    "no projects preserves legacy site routes",
			routes:  routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr: false,
		},
		{
			name:     "valid github project",
			projects: projects(portfolio("portfolio", "portfolio", "octaviosantacruz.com", "refs/heads/main", 123)),
			routes:   routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr:  false,
		},
		{
			name:     "valid tag ref",
			projects: projects(portfolio("portfolio", "portfolio", "octaviosantacruz.com", "refs/tags/v1.0.0", 123)),
			routes:   routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr:  false,
		},
		{
			name:     "hostname comparison uses canonical host form",
			projects: projects(portfolio("portfolio", "portfolio", "OCTAVIOSANTACRUZ.COM.", "refs/heads/main", 123)),
			routes:   routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr:  false,
		},
		{
			name: "valid token reference",
			projects: projects(Project{
				Name: "portfolio", Site: "portfolio", Hostnames: []string{"octaviosantacruz.com"},
				Deployers: []ProjectDeployer{
					{Token: "file:/run/secrets/portfolio-deploy.token"},
					{Token: "env:PORTFOLIO_DEPLOY_TOKEN"},
				},
			}),
			routes:  routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr: false,
		},
		{
			name: "duplicate project names",
			projects: projects(
				portfolio("portfolio", "portfolio", "octaviosantacruz.com", "refs/heads/main", 123),
				portfolio("portfolio", "docs", "docs.example.test", "refs/heads/main", 124),
			),
			routes: routes(
				Route{Host: "octaviosantacruz.com", Site: "portfolio"},
				Route{Host: "docs.example.test", Site: "docs"},
			),
			wantErr: true,
		},
		{
			name: "two projects cannot claim one site",
			projects: projects(
				portfolio("portfolio", "portfolio", "octaviosantacruz.com", "refs/heads/main", 123),
				portfolio("portfolio-preview", "portfolio", "preview.example.test", "refs/heads/main", 124),
			),
			routes: routes(
				Route{Host: "octaviosantacruz.com", Site: "portfolio"},
				Route{Host: "preview.example.test", Site: "portfolio"},
			),
			wantErr: true,
		},
		{
			name: "same github repository and ref cannot deploy two projects",
			projects: projects(
				portfolio("portfolio", "portfolio", "octaviosantacruz.com", "refs/heads/main", 123),
				portfolio("docs", "docs", "docs.example.test", "refs/heads/main", 123),
			),
			routes: routes(
				Route{Host: "octaviosantacruz.com", Site: "portfolio"},
				Route{Host: "docs.example.test", Site: "docs"},
			),
			wantErr: true,
		},
		{
			name:     "hostname without a route is rejected",
			projects: projects(portfolio("portfolio", "portfolio", "missing.example.test", "refs/heads/main", 123)),
			routes:   routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr:  true,
		},
		{
			name:     "hostname routed to another site is rejected",
			projects: projects(portfolio("portfolio", "portfolio", "octaviosantacruz.com", "refs/heads/main", 123)),
			routes:   routes(Route{Host: "octaviosantacruz.com", Site: "docs"}),
			wantErr:  true,
		},
		{
			name:     "no deployers is rejected",
			projects: projects(Project{Name: "portfolio", Site: "portfolio", Hostnames: []string{"octaviosantacruz.com"}}),
			routes:   routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr:  true,
		},
		{
			name:     "empty deployer is rejected",
			projects: projects(Project{Name: "portfolio", Site: "portfolio", Hostnames: []string{"octaviosantacruz.com"}, Deployers: []ProjectDeployer{{}}}),
			routes:   routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr:  true,
		},
		{
			name: "empty environment secret reference is rejected",
			projects: projects(Project{
				Name: "portfolio", Site: "portfolio", Hostnames: []string{"octaviosantacruz.com"},
				Deployers: []ProjectDeployer{{Token: "env:  "}},
			}),
			routes:  routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr: true,
		},
		{
			name: "empty file secret reference is rejected",
			projects: projects(Project{
				Name: "portfolio", Site: "portfolio", Hostnames: []string{"octaviosantacruz.com"},
				Deployers: []ProjectDeployer{{Token: "file:  "}},
			}),
			routes:  routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr: true,
		},
		{
			name: "deployer cannot configure both github and token",
			projects: projects(Project{
				Name: "portfolio", Site: "portfolio", Hostnames: []string{"octaviosantacruz.com"},
				Deployers: []ProjectDeployer{{
					GitHub: &ProjectGitHubTrust{RepositoryID: 123, RepositoryOwnerID: 42, Ref: "refs/heads/main"},
					Token:  "file:/run/secrets/portfolio-deploy.token",
				}},
			}),
			routes:  routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr: true,
		},
		{
			name:               "inline deploy token is rejected",
			projects:           projects(Project{Name: "portfolio", Site: "portfolio", Hostnames: []string{"octaviosantacruz.com"}, Deployers: []ProjectDeployer{{Token: "inline-secret-must-not-leak"}}}),
			routes:             routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr:            true,
			forbiddenErrorText: "inline-secret-must-not-leak",
		},
		{
			name:     "ref must name a branch or tag",
			projects: projects(portfolio("portfolio", "portfolio", "octaviosantacruz.com", "main", 123)),
			routes:   routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr:  true,
		},
		{
			name:     "ref branch prefix must have a name after it",
			projects: projects(portfolio("portfolio", "portfolio", "octaviosantacruz.com", "refs/heads/", 123)),
			routes:   routes(Route{Host: "octaviosantacruz.com", Site: "portfolio"}),
			wantErr:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projectsBefore := cloneProjectFixtures(test.projects)
			routesBefore := slices.Clone(test.routes)
			err := ValidateProjects(test.projects, test.routes)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateProjects() error = %v, wantErr %t", err, test.wantErr)
			}
			if test.forbiddenErrorText != "" && err != nil && strings.Contains(err.Error(), test.forbiddenErrorText) {
				t.Fatal("validation error echoed an inline project token")
			}
			if !reflect.DeepEqual(test.projects, projectsBefore) || !reflect.DeepEqual(test.routes, routesBefore) {
				t.Fatal("ValidateProjects mutated its inputs")
			}
		})
	}
}

func TestLegacySiteRoutesRemainValidWithoutProjects(t *testing.T) {
	for _, test := range []struct {
		name     string
		projects []Project
	}{
		{name: "omitted"},
		{name: "empty", projects: []Project{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			validated, err := Validate(Config{
				Listen:   ":8888",
				Pages:    &Pages{Dir: t.TempDir()},
				Projects: test.projects,
				Routes:   []Route{{Host: "blog.localhost", Site: "blog"}},
			})
			if err != nil {
				t.Fatalf("Validate legacy site route: %v", err)
			}
			if len(validated.Projects) != 0 || len(validated.Routes) != 1 || validated.Routes[0].Destination() != "site:blog" {
				t.Fatalf("validated config = %+v, want the existing site route unchanged", validated)
			}
		})
	}
}

func TestValidateNormalizesProjectHostnamesOnTheReturnedCopy(t *testing.T) {
	input := Config{
		Listen: ":8888",
		Pages:  &Pages{Dir: t.TempDir()},
		Projects: []Project{{
			Name: "portfolio", Site: "portfolio", Hostnames: []string{"PORTFOLIO.EXAMPLE."},
			Deployers: []ProjectDeployer{{GitHub: &ProjectGitHubTrust{RepositoryID: 123, RepositoryOwnerID: 42, Ref: "refs/heads/main"}}},
		}},
		Routes: []Route{{Host: "portfolio.example", Site: "portfolio"}},
	}
	validated, err := Validate(input)
	if err != nil {
		t.Fatalf("Validate project hostname: %v", err)
	}
	if got := validated.Projects[0].Hostnames[0]; got != "portfolio.example" {
		t.Fatalf("normalized project hostname = %q, want portfolio.example", got)
	}
	if got := input.Projects[0].Hostnames[0]; got != "PORTFOLIO.EXAMPLE." {
		t.Fatalf("Validate mutated its input project hostname to %q", got)
	}
}

func TestCloneCopiesProjectsDeeply(t *testing.T) {
	source := Config{Projects: []Project{{
		Name: "portfolio", Site: "portfolio", Hostnames: []string{"portfolio.example"},
		Deployers: []ProjectDeployer{{GitHub: &ProjectGitHubTrust{RepositoryID: 1, RepositoryOwnerID: 2, Ref: "refs/heads/main"}}},
	}}}
	cloned := Clone(source)
	cloned.Projects[0].Hostnames[0] = "changed.example"
	cloned.Projects[0].Deployers[0].GitHub.Ref = "refs/heads/changed"
	if source.Projects[0].Hostnames[0] != "portfolio.example" || source.Projects[0].Deployers[0].GitHub.Ref != "refs/heads/main" {
		t.Fatal("mutating a cloned config changed the source project's nested fields")
	}
}

func cloneProjectFixtures(projects []Project) []Project {
	if projects == nil {
		return nil
	}
	cloned := make([]Project, len(projects))
	for i, project := range projects {
		cloned[i] = project
		cloned[i].Hostnames = slices.Clone(project.Hostnames)
		cloned[i].Deployers = slices.Clone(project.Deployers)
		for j := range cloned[i].Deployers {
			if github := project.Deployers[j].GitHub; github != nil {
				githubCopy := *github
				cloned[i].Deployers[j].GitHub = &githubCopy
			}
		}
	}
	return cloned
}
