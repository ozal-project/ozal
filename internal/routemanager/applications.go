package routemanager

import (
	"errors"
	"github.com/ozal-project/ozal/internal/config"
)

var ErrConfigChanged = errors.New("configuration changed; refresh and review again")

// PutApplication creates or updates one application, preserving project/domain
// associations. A reviewed snapshot digest is mandatory to prevent stale edits.
func (m *Manager) PutApplication(route config.Route, expected string) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if err := m.contextError(); err != nil {
		return err
	}
	current := m.current.Load()
	if current == nil || expected == "" || current.digest != expected {
		return ErrConfigChanged
	}
	next, err := config.WithApplication(current.cfg, route)
	if err != nil {
		return err
	}
	return m.persistAndInstall(next)
}

// PutProject changes publisher trust as one validated configuration mutation.
// Removing a project revokes future deploy authentication but retains content.
func (m *Manager) PutProject(project config.Project, expected string, remove bool) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if err := m.contextError(); err != nil {
		return err
	}
	current := m.current.Load()
	if current == nil || expected == "" || current.digest != expected {
		return ErrConfigChanged
	}
	cfg := config.Clone(current.cfg)
	found := false
	projects := make([]config.Project, 0, len(cfg.Projects)+1)
	for _, existing := range cfg.Projects {
		if existing.Name == project.Name {
			found = true
			if !remove {
				projects = append(projects, project)
			}
		} else {
			projects = append(projects, existing)
		}
	}
	if !found && !remove {
		projects = append(projects, project)
	}
	cfg.Projects = projects
	return m.persistAndInstall(cfg)
}

func (m *Manager) ConfigureGitHub(app *config.GitHubApp, expected string) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if err := m.contextError(); err != nil {
		return err
	}
	current := m.current.Load()
	if current == nil || expected == "" || current.digest != expected {
		return ErrConfigChanged
	}
	cfg := config.Clone(current.cfg)
	cfg.GitHub = app
	return m.persistAndInstall(cfg)
}

func (m *Manager) RemoveApplication(host, expected string) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if err := m.contextError(); err != nil {
		return err
	}
	current := m.current.Load()
	if current == nil || expected == "" || current.digest != expected {
		return ErrConfigChanged
	}
	cfg, err := config.WithoutApplication(current.cfg, host)
	if err != nil {
		return err
	}
	return m.persistAndInstall(cfg)
}

// AddZone adds a runtime zone (EDGE-008) under the reviewed digest.
func (m *Manager) AddZone(name, expected string) error {
	return m.mutate(expected, func(cfg config.Config) (config.Config, error) { return config.WithZone(cfg, name) })
}

// RemoveZone removes a runtime zone under the reviewed digest; a zone that
// still serves an application is refused by config.WithoutZone.
func (m *Manager) RemoveZone(name, expected string) error {
	return m.mutate(expected, func(cfg config.Config) (config.Config, error) { return config.WithoutZone(cfg, name) })
}

// RenameProject renames a project and its site, moving the routes that
// served it, under the reviewed digest (config.PlanProjectRename).
func (m *Manager) RenameProject(from, to, expected string) error {
	return m.mutate(expected, func(cfg config.Config) (config.Config, error) {
		next, _, _, err := config.PlanProjectRename(cfg, from, to)
		return next, err
	})
}

// mutate applies one reviewed change: the digest the operator reviewed must
// still be the one in effect.
func (m *Manager) mutate(expected string, change func(config.Config) (config.Config, error)) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if err := m.contextError(); err != nil {
		return err
	}
	current := m.current.Load()
	if current == nil || expected == "" || current.digest != expected {
		return ErrConfigChanged
	}
	next, err := change(current.cfg)
	if err != nil {
		return err
	}
	return m.persistAndInstall(next)
}
