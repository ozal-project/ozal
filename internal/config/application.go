package config

import "fmt"

// WithApplication validates a complete candidate without mutating live config
// or resolving secrets. Adding a domain to a project reuses that site's policy
// only when explicitly supplied by the operator; private is the UI default.
func WithApplication(current Config, route Route) (Config, error) {
	host, err := NormalizeHost(route.Host)
	if err != nil {
		return Config{}, fmt.Errorf("application hostname is invalid")
	}
	route.Host = host
	next := Clone(current)
	replaced := false
	for i, existing := range next.Routes {
		if existing.Host == host {
			next.Routes[i] = route
			replaced = true
			break
		}
	}
	if !replaced {
		next.Routes = append(next.Routes, route)
	}
	for i, project := range next.Projects {
		hosts := make([]string, 0, len(project.Hostnames)+1)
		for _, existing := range project.Hostnames {
			if existing != host {
				hosts = append(hosts, existing)
			}
		}
		if project.Site == route.Site {
			hosts = append(hosts, host)
		}
		next.Projects[i].Hostnames = hosts
	}
	validated, err := Validate(next)
	if err != nil {
		return Config{}, err
	}
	return *validated, nil
}

func WithoutApplication(current Config, rawHost string) (Config, error) {
	host, err := NormalizeHost(rawHost)
	if err != nil {
		return Config{}, err
	}
	next := Clone(current)
	routes := make([]Route, 0, len(next.Routes))
	found := false
	for _, route := range next.Routes {
		if route.Host == host {
			found = true
		} else {
			routes = append(routes, route)
		}
	}
	if !found {
		return Config{}, fmt.Errorf("application does not exist")
	}
	next.Routes = routes
	for i, p := range next.Projects {
		hosts := make([]string, 0, len(p.Hostnames))
		for _, h := range p.Hostnames {
			if h != host {
				hosts = append(hosts, h)
			}
		}
		next.Projects[i].Hostnames = hosts
	}
	validated, err := Validate(next)
	if err != nil {
		return Config{}, err
	}
	return *validated, nil
}
