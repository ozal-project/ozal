package config

import "fmt"

// ValidateProjectGitHubTrust checks an explicit GitHub publisher binding.
func ValidateProjectGitHubTrust(trust ProjectGitHubTrust) error {
	if trust.RepositoryID < 1 || trust.RepositoryOwnerID < 1 {
		return fmt.Errorf("repository and owner IDs must be positive")
	}
	if !validProjectRef(trust.Ref) {
		return fmt.Errorf("ref must start with refs/heads/ or refs/tags/ and include a name")
	}
	if !validWorkflowRef(trust.WorkflowRef) {
		return fmt.Errorf("workflow_ref must name the deploy workflow as owner/repo/.github/workflows/FILE.yml@refs/heads/BRANCH")
	}
	return nil
}

// PlanProjectTrust replaces the one GitHub publisher of an existing project.
// Other deployers, hostnames, routes and projects retain their authority.
// This is an explicit host-operator binding, independent of App discovery.
func PlanProjectTrust(cfg Config, name string, trust ProjectGitHubTrust) (Project, ProjectGitHubTrust, error) {
	candidate := Clone(cfg)
	for i := range candidate.Projects {
		project := &candidate.Projects[i]
		if project.Name != name {
			continue
		}
		index := -1
		var previous ProjectGitHubTrust
		for j, deployer := range project.Deployers {
			if deployer.GitHub != nil {
				if index >= 0 {
					return Project{}, previous, fmt.Errorf("project must have exactly one GitHub publisher")
				}
				index, previous = j, *deployer.GitHub
			}
		}
		if index < 0 {
			return Project{}, previous, fmt.Errorf("project must have exactly one GitHub publisher")
		}
		project.Deployers[index].GitHub = &trust
		if _, err := Validate(candidate); err != nil {
			return Project{}, previous, err
		}
		return *project, previous, nil
	}
	return Project{}, ProjectGitHubTrust{}, fmt.Errorf("project not found")
}
