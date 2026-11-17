package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A team, as registered in the registry's teams.json
type Team struct {
	Name    string   `json:"name"`
	Repo    string   `json:"repo"`
	Members []string `json:"members,omitempty"`
	// Optional: a tag, branch or commit to use instead of the latest tag on main
	Ref string `json:"ref,omitempty"`
	// Optional: the Dockerfile and build context, relative to the repository
	// root (defaults: Dockerfile, and the root)
	Dockerfile string `json:"dockerfile,omitempty"`
	Context    string `json:"context,omitempty"`
}

var validTeamName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

func ParseTeams(data []byte) ([]Team, error) {
	var teams []Team
	if err := json.Unmarshal(data, &teams); err != nil {
		return nil, err
	}

	names := make(map[string]bool)
	for _, team := range teams {
		if !validTeamName.MatchString(team.Name) {
			return nil, fmt.Errorf("invalid team name %q (letters, digits, - and _, at most 32)", team.Name)
		}
		lower := strings.ToLower(team.Name)
		if names[lower] {
			return nil, fmt.Errorf("duplicate team name %q", team.Name)
		}
		names[lower] = true
		if team.Repo == "" {
			return nil, fmt.Errorf("team %s has no repository", team.Name)
		}
		if strings.Contains(team.Context, "..") || strings.Contains(team.Dockerfile, "..") {
			return nil, fmt.Errorf("team %s: invalid dockerfile or context path", team.Name)
		}
	}
	return teams, nil
}

type RegistryConfig struct {
	// Either a git repository holding the registry file...
	URL    string `json:"url,omitempty"`
	Branch string `json:"branch,omitempty"` // default: main
	File   string `json:"file,omitempty"`   // default: teams.json
	// ...or a local file
	Path string `json:"path,omitempty"`
}

// LoadTeams reads the registry, from its git repository (mirrored in
// dataDir) or from a local file
func LoadTeams(ctx context.Context, config RegistryConfig, dataDir string) ([]Team, error) {
	if config.URL == "" {
		if config.Path == "" {
			return nil, nil
		}
		data, err := os.ReadFile(config.Path)
		if err != nil {
			return nil, err
		}
		return ParseTeams(data)
	}

	branch := config.Branch
	if branch == "" {
		branch = "main"
	}
	file := config.File
	if file == "" {
		file = "teams.json"
	}

	dir := filepath.Join(dataDir, "registry.git")
	if err := Mirror(ctx, config.URL, dir); err != nil {
		return nil, fmt.Errorf("updating the registry: %w", err)
	}
	data, err := git(ctx, dir, "show", "refs/heads/"+branch+":"+file)
	if err != nil {
		return nil, err
	}
	return ParseTeams([]byte(data))
}
