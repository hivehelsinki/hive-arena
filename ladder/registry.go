package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	// root (defaults: the root, and the Dockerfile at the top of the context).
	// The Dockerfile must be inside the context.
	Dockerfile string `json:"dockerfile,omitempty"`
	Context    string `json:"context,omitempty"`
}

var validTeamName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// ParseTeams parses a registry file. Invalid entries are left out, each with
// an error (problems), so that one bad entry does not affect the other teams.
// Teams of a public registry (remote) must use https repositories.
func ParseTeams(data []byte, remote bool) (teams []Team, problems []error, err error) {
	var entries []Team
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, nil, err
	}

	names := make(map[string]bool)
	for _, team := range entries {
		if err := validateTeam(team, remote); err != nil {
			problems = append(problems, err)
			continue
		}
		lower := strings.ToLower(team.Name)
		if names[lower] {
			problems = append(problems, fmt.Errorf("duplicate team name %q: only the first one is used", team.Name))
			continue
		}
		names[lower] = true
		teams = append(teams, team)
	}
	return teams, problems, nil
}

func validateTeam(team Team, remote bool) error {
	if !validTeamName.MatchString(team.Name) {
		return fmt.Errorf("invalid team name %q (letters, digits, - and _, at most 32)", team.Name)
	}
	if team.Repo == "" {
		return fmt.Errorf("team %s has no repository", team.Name)
	}
	// Other protocols could reach the machine's files or internal services
	if remote && !strings.HasPrefix(team.Repo, "https://") {
		return fmt.Errorf("team %s: the repository must be an https:// URL", team.Name)
	}
	if strings.HasPrefix(team.Repo, "-") || strings.HasPrefix(team.Ref, "-") {
		return fmt.Errorf("team %s: invalid repository or ref", team.Name)
	}
	if strings.Contains(team.Context, "..") || strings.Contains(team.Dockerfile, "..") {
		return fmt.Errorf("team %s: invalid dockerfile or context path", team.Name)
	}
	if _, err := contextDockerfile(team); err != nil {
		return fmt.Errorf("team %s: %w", team.Name, err)
	}
	return nil
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
// dataDir) or from a local file. Problems are reported without failing when
// possible: invalid entries are left out, and when the registry cannot be
// fetched or is not valid JSON, its last known valid version is used.
func LoadTeams(ctx context.Context, config RegistryConfig, dataDir string) ([]Team, []string, error) {
	if config.URL == "" {
		if config.Path == "" {
			return nil, nil, fmt.Errorf("no registry configured (registry.url or registry.path)")
		}
		data, err := os.ReadFile(config.Path)
		if err != nil {
			return nil, nil, err
		}
		teams, problems, err := ParseTeams(data, false)
		return teams, errorStrings(problems), err
	}

	branch := config.Branch
	if branch == "" {
		branch = "main"
	}
	file := config.File
	if file == "" {
		file = "teams.json"
	}

	var problems []string
	dir := filepath.Join(dataDir, "registry.git")
	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	err := Mirror(fetchCtx, config.URL, dir)
	cancel()
	if err != nil {
		problems = append(problems, fmt.Sprintf("could not update the registry, using the last fetched version: %s", err))
	}

	lastValid := filepath.Join(dataDir, "registry-last.json")
	data, err := git(ctx, dir, "show", "refs/heads/"+branch+":"+file)
	var teams []Team
	var invalid []error
	if err == nil {
		teams, invalid, err = ParseTeams([]byte(data), true)
	}
	if err != nil {
		problems = append(problems, fmt.Sprintf("invalid registry, using the last valid version: %s", err))
		last, lerr := os.ReadFile(lastValid)
		if lerr != nil {
			return nil, problems, fmt.Errorf("reading the registry: %w", err)
		}
		teams, invalid, err = ParseTeams(last, true)
		if err != nil {
			return nil, problems, err
		}
	} else if err := writeFileDurably(lastValid, []byte(data)); err != nil {
		log.Printf("Could not save the registry: %s", err)
	}
	return teams, append(problems, errorStrings(invalid)...), nil
}

func errorStrings(errs []error) []string {
	var strs []string
	for _, err := range errs {
		strs = append(strs, err.Error())
	}
	return strs
}
