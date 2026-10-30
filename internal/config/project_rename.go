package config

import (
	"fmt"
	"slices"
)

// PlanProjectRename renames a project and its site together: every route
// that served the old site serves the new name, and hostnames and publisher
// trust stay as they are. The new name must be free: no project, project
// site or route may use it already. It returns the next configuration, the
// renamed project and the hosts whose route changes.
func PlanProjectRename(cfg Config, from, to string) (Config, Project, []string, error) {
	if !siteNamePattern.MatchString(to) {
		return Config{}, Project{}, nil, fmt.Errorf("new name %q must be a lowercase DNS label of 1 to 63 characters", to)
	}
	if from == to {
		return Config{}, Project{}, nil, fmt.Errorf("the project is already called %q", to)
	}
	index := slices.IndexFunc(cfg.Projects, func(p Project) bool { return p.Name == from })
	if index < 0 {
		return Config{}, Project{}, nil, fmt.Errorf("project %q not found", from)
	}
	for _, p := range cfg.Projects {
		if p.Name == to || p.Site == to {
			return Config{}, Project{}, nil, fmt.Errorf("%q is already used by project %q", to, p.Name)
		}
	}
	for _, r := range cfg.Routes {
		if r.Site == to {
			return Config{}, Project{}, nil, fmt.Errorf("site %q is already served at %s", to, r.Host)
		}
	}
	next := Clone(cfg)
	next.Routes = slices.Clone(cfg.Routes)
	next.Projects = CloneProjects(cfg.Projects)
	old := next.Projects[index].Site
	var hosts []string
	for i := range next.Routes {
		if next.Routes[i].Site == old {
			next.Routes[i].Site = to
			hosts = append(hosts, next.Routes[i].Host)
		}
	}
	next.Projects[index].Name, next.Projects[index].Site = to, to
	validated, err := Validate(next)
	if err != nil {
		return Config{}, Project{}, nil, err
	}
	return *validated, validated.Projects[index], hosts, nil
}
