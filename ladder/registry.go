package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// A team, as registered in the registry's teams.json
type Team struct {
	// The name teams choose: any text, displayed as it is
	Name string `json:"name"`
	// The identifier made from the name (see TeamID): lowercase letters and
	// digits only, unique. It is what the ladder uses wherever the name could
	// cause trouble: the database, files, docker images, URLs, the name the
	// bot joins its game with. Never read from the registry file.
	ID      string   `json:"id"`
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

// The longest team name, in characters
const maxTeamNameLength = 64

// TeamID makes the identifier of a team from its name: the name without
// accents and without anything but ASCII letters and digits, in lowercase
// (at most 24 characters, "team" if nothing is left), followed by 6
// hexadecimal digits of a hash of the whole name, so that names that look
// alike (other alphabets, symbols) get different identifiers. Names that
// differ only by case, spacing or Unicode normalization get the same one.
// It depends on nothing but the name: it does not change when other teams
// are added, removed or reordered.
func TeamID(name string) string {
	var slug []rune
	for _, r := range norm.NFKD.String(name) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			slug = append(slug, unicode.ToLower(r))
		}
	}
	if len(slug) > 24 {
		slug = slug[:24]
	}
	if len(slug) == 0 {
		slug = []rune("team")
	}
	key := strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(name)), " "))
	sum := sha256.Sum256([]byte(key))
	return string(slug) + hex.EncodeToString(sum[:3])
}

// ParseTeams parses a registry file. Invalid entries are left out, each with
// an error (problems), so that one bad entry does not affect the other teams.
// Teams of a public registry (remote) must use https repositories. Each team
// gets its identifier (TeamID).
func ParseTeams(data []byte, remote bool) (teams []Team, problems []error, err error) {
	var entries []Team
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, nil, err
	}

	names := make(map[string]string) // identifier -> name
	for _, team := range entries {
		team.Name = strings.TrimSpace(team.Name)
		if err := validateTeam(team, remote); err != nil {
			problems = append(problems, err)
			continue
		}
		team.ID = TeamID(team.Name)
		if first, ok := names[team.ID]; ok {
			problems = append(problems, fmt.Errorf("duplicate team %q: the same as %q, only the first one is used", team.Name, first))
			continue
		}
		names[team.ID] = team.Name
		teams = append(teams, team)
	}
	return teams, problems, nil
}

// A name is any text, except what could confuse readers or the logs: control
// characters, and invisible ones (format characters: zero-width spaces, marks
// that change the direction of the text...), so that two names that look the
// same are the same name. The zero-width joiner stays allowed: it is part of
// emoji (👩‍🚀).
func validTeamName(name string) error {
	if name == "" {
		return fmt.Errorf("a team has no name")
	}
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxTeamNameLength {
		return fmt.Errorf("invalid team name %q (valid text, at most %d characters)", name, maxTeamNameLength)
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) && r != '\u200d' {
			return fmt.Errorf("invalid team name %q (no control or invisible characters)", name)
		}
	}
	return nil
}

func validateTeam(team Team, remote bool) error {
	if err := validTeamName(team.Name); err != nil {
		return err
	}
	if team.Repo == "" {
		return fmt.Errorf("team %q has no repository", team.Name)
	}
	// Other protocols could reach the machine's files or internal services
	if remote && !strings.HasPrefix(team.Repo, "https://") {
		return fmt.Errorf("team %q: the repository must be an https:// URL", team.Name)
	}
	if strings.HasPrefix(team.Repo, "-") || strings.HasPrefix(team.Ref, "-") {
		return fmt.Errorf("team %q: invalid repository or ref", team.Name)
	}
	if strings.Contains(team.Context, "..") || strings.Contains(team.Dockerfile, "..") {
		return fmt.Errorf("team %q: invalid dockerfile or context path", team.Name)
	}
	if _, err := contextDockerfile(team); err != nil {
		return fmt.Errorf("team %q: %w", team.Name, err)
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
