package config

import (
	"fmt"
	"strings"
	"unicode"
)

// Project binds one deployed Pages site to the hostnames that serve it and
// the identities allowed to publish new deployments.
type Project struct {
	Name      string            `json:"name"`
	Site      string            `json:"site"`
	Hostnames []string          `json:"hostnames"`
	Deployers []ProjectDeployer `json:"deployers"`
}

// ProjectDeployer is one way to authenticate a publisher. Exactly one of
// GitHub or Token is expected. Token contains only an env:/file: reference.
type ProjectDeployer struct {
	GitHub *ProjectGitHubTrust `json:"github,omitempty"`
	Token  string              `json:"token,omitempty"`
}

// ProjectGitHubTrust pins a GitHub repository and ref. Repository IDs are
// numeric GitHub IDs, not mutable owner or repository names.
type ProjectGitHubTrust struct {
	RepositoryID      int64  `json:"repository_id"`
	RepositoryOwnerID int64  `json:"repository_owner_id"`
	Ref               string `json:"ref"`
	WorkflowRef       string `json:"workflow_ref,omitempty"`
}

// ValidateProjects is the pure cross-check between project trust and site
// routes. It never resolves token references or changes either input.
func ValidateProjects(projects []Project, routes []Route) error {
	if len(projects) == 0 {
		return nil
	}

	routeSites := make(map[string]string, len(routes))
	for _, route := range routes {
		host, err := NormalizeHost(route.Host)
		if err != nil {
			return fmt.Errorf("project validation: route host is invalid: %w", err)
		}
		if _, exists := routeSites[host]; exists {
			return fmt.Errorf("project validation: duplicate route host %q", host)
		}
		routeSites[host] = route.Site
	}

	projectNames := make(map[string]struct{}, len(projects))
	projectSites := make(map[string]struct{}, len(projects))
	type githubTrust struct {
		repositoryID int64
		ref          string
	}
	githubProjects := make(map[githubTrust]string)

	for i, project := range projects {
		field := fmt.Sprintf("projects[%d]", i)
		if !siteNamePattern.MatchString(project.Name) {
			return fmt.Errorf("%s.name must be a lowercase DNS label of 1 to 63 characters", field)
		}
		if _, exists := projectNames[project.Name]; exists {
			return fmt.Errorf("%s.name duplicates another project", field)
		}
		projectNames[project.Name] = struct{}{}

		if !siteNamePattern.MatchString(project.Site) {
			return fmt.Errorf("%s.site must be a lowercase DNS label of 1 to 63 characters", field)
		}
		if _, exists := projectSites[project.Site]; exists {
			return fmt.Errorf("%s.site is already assigned to another project", field)
		}
		projectSites[project.Site] = struct{}{}

		if len(project.Hostnames) == 0 {
			return fmt.Errorf("%s.hostnames must contain at least one route hostname", field)
		}
		projectHosts := make(map[string]struct{}, len(project.Hostnames))
		for j, rawHost := range project.Hostnames {
			host, err := NormalizeHost(rawHost)
			if err != nil {
				return fmt.Errorf("%s.hostnames[%d] is invalid: %w", field, j, err)
			}
			if _, exists := projectHosts[host]; exists {
				return fmt.Errorf("%s.hostnames[%d] duplicates a hostname", field, j)
			}
			projectHosts[host] = struct{}{}

			site, exists := routeSites[host]
			if !exists {
				return fmt.Errorf("%s.hostnames[%d] has no route", field, j)
			}
			if site != project.Site {
				return fmt.Errorf("%s.hostnames[%d] routes to site %q, not project site %q", field, j, site, project.Site)
			}
		}

		if len(project.Deployers) == 0 {
			return fmt.Errorf("%s.deployers must contain at least one publisher", field)
		}
		for j, deployer := range project.Deployers {
			deployerField := fmt.Sprintf("%s.deployers[%d]", field, j)
			if deployer.GitHub == nil && deployer.Token == "" {
				return fmt.Errorf("%s must configure github or a token reference", deployerField)
			}
			if deployer.GitHub != nil && deployer.Token != "" {
				return fmt.Errorf("%s must configure exactly one authentication method", deployerField)
			}
			if deployer.GitHub != nil {
				github := deployer.GitHub
				if github.RepositoryID < 1 || github.RepositoryOwnerID < 1 {
					return fmt.Errorf("%s.github repository and owner IDs must be positive", deployerField)
				}
				if !validProjectRef(github.Ref) {
					return fmt.Errorf("%s.github.ref must start with refs/heads/ or refs/tags/ and include a name", deployerField)
				}
				if github.WorkflowRef != "" && (strings.TrimSpace(github.WorkflowRef) != github.WorkflowRef || strings.IndexFunc(github.WorkflowRef, unicode.IsControl) >= 0) {
					return fmt.Errorf("%s.github.workflow_ref must not contain surrounding whitespace or control characters", deployerField)
				}

				key := githubTrust{repositoryID: github.RepositoryID, ref: github.Ref}
				if previousProject, exists := githubProjects[key]; exists && previousProject != project.Name {
					return fmt.Errorf("%s.github repository and ref are already trusted by another project", deployerField)
				}
				githubProjects[key] = project.Name
				continue
			}

			if err := validateProjectTokenReference(deployer.Token); err != nil {
				return fmt.Errorf("%s.token %w", deployerField, err)
			}
		}
	}
	return nil
}

func validProjectRef(ref string) bool {
	if strings.TrimSpace(ref) != ref || strings.IndexFunc(ref, unicode.IsSpace) >= 0 || strings.IndexFunc(ref, unicode.IsControl) >= 0 {
		return false
	}
	for _, prefix := range []string{"refs/heads/", "refs/tags/"} {
		if strings.HasPrefix(ref, prefix) && len(ref) > len(prefix) {
			return true
		}
	}
	return false
}

func validateProjectTokenReference(reference string) error {
	var value string
	switch {
	case strings.HasPrefix(reference, "env:"):
		value = strings.TrimSpace(strings.TrimPrefix(reference, "env:"))
		if value == "" {
			return fmt.Errorf("must name an environment variable")
		}
	case strings.HasPrefix(reference, "file:"):
		value = strings.TrimSpace(strings.TrimPrefix(reference, "file:"))
		if value == "" {
			return fmt.Errorf("must name a secret file")
		}
	default:
		return fmt.Errorf("must use an env: or file: secret reference")
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("must not contain control characters")
	}
	return nil
}
