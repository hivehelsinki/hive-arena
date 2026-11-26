package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// testRepo is a local git repository used as a team's remote
type testRepo struct {
	t   *testing.T
	dir string
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	repo := &testRepo{t, t.TempDir()}
	repo.git("init", "-q", "-b", "main")
	repo.git("config", "user.email", "test@example.com")
	repo.git("config", "user.name", "test")
	return repo
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %s\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes files and commits them, at a given time (seconds since 2026)
func (r *testRepo) commit(files map[string]string, at int) string {
	r.t.Helper()
	for name, content := range files {
		path := filepath.Join(r.dir, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	r.git("add", ".")
	cmd := exec.Command("git", "commit", "-q", "-m", "commit")
	cmd.Dir = r.dir
	date := "@" + strconv.Itoa(1767225600+at) + " +0000"
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+date, "GIT_AUTHOR_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("commit: %s\n%s", err, out)
	}
	return r.git("rev-parse", "HEAD")
}

func TestLatestTag(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	c1 := repo.commit(map[string]string{"a": "1"}, 10)
	repo.git("tag", "v1", c1)
	c2 := repo.commit(map[string]string{"a": "2"}, 20)
	repo.git("tag", "-a", "-m", "annotated", "v2", c2)
	repo.commit(map[string]string{"a": "3"}, 30) // untagged

	// A more recent tag, but on another branch
	repo.git("checkout", "-q", "-b", "experiment")
	c4 := repo.commit(map[string]string{"a": "4"}, 40)
	repo.git("tag", "v-experiment", c4)
	repo.git("checkout", "-q", "main")

	mirror := filepath.Join(t.TempDir(), "mirror.git")
	if err := Mirror(ctx, repo.dir, mirror); err != nil {
		t.Fatal(err)
	}

	tag, err := LatestTag(ctx, mirror, "main")
	if err != nil {
		t.Fatal(err)
	}
	if tag.Name != "v2" || tag.Commit != c2 {
		t.Errorf("expected v2 at %s, got %+v", c2, tag)
	}

	// New tag on main, picked up by the next fetch
	c5 := repo.commit(map[string]string{"a": "5"}, 50)
	repo.git("tag", "v3", c5)
	// Moving a tag is fine too
	repo.git("tag", "-f", "v1", c5)
	if err := Mirror(ctx, repo.dir, mirror); err != nil {
		t.Fatal(err)
	}
	tag, _ = LatestTag(ctx, mirror, "main")
	if tag.Commit != c5 || (tag.Name != "v3" && tag.Name != "v1") {
		t.Errorf("expected a tag at %s, got %+v", c5, tag)
	}

	if commit, err := ResolveRef(ctx, mirror, "v2"); err != nil || commit != c2 {
		t.Errorf("resolving v2: %s, %v", commit, err)
	}
	if _, err := ResolveRef(ctx, mirror, "nope"); err == nil {
		t.Error("resolving an unknown ref should fail")
	}
}

func TestLatestTagNone(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)
	repo.commit(map[string]string{"a": "1"}, 10)

	mirror := filepath.Join(t.TempDir(), "mirror.git")
	if err := Mirror(ctx, repo.dir, mirror); err != nil {
		t.Fatal(err)
	}
	if _, err := LatestTag(ctx, mirror, "main"); err == nil {
		t.Error("expected an error without tags")
	}
}

func TestParseTeams(t *testing.T) {
	valid := `[{"name": "Super-Team_1", "repo": "https://example.com/a.git", "members": ["x", "y", "z", "u", "v", "w"]}, {"name": "b", "repo": "https://example.com/b", "ref": "v1", "dockerfile": "bot/Dockerfile", "context": "bot"}]`
	teams, problems, err := ParseTeams([]byte(valid), true)
	if err != nil || len(problems) != 0 || len(teams) != 2 || teams[1].Ref != "v1" {
		t.Errorf("unexpected result: %+v, %v, %v", teams, problems, err)
	}

	// Invalid entries are left out, without affecting the others
	invalid := []string{
		`{"name": "", "repo": "https://r"}`,
		`{"name": "  ", "repo": "https://r"}`,
		`{"name": "line\nbreak", "repo": "https://r"}`,
		`{"name": "evil\u202etext", "repo": "https://r"}`,
		`{"name": "zero\u200bwidth", "repo": "https://r"}`,
		`{"name": "mark\u200e", "repo": "https://r"}`,
		`{"name": "` + strings.Repeat("x", 65) + `", "repo": "https://r"}`,
		`{"name": "c", "repo": ""}`,
		`{"name": " super-TEAM_1 ", "repo": "https://r"}`,
		`{"name": "SUPER-Team_1", "id": "other", "repo": "https://r"}`,
		`{"name": "c", "repo": "https://r", "context": "../x"}`,
		`{"name": "c", "repo": "/home/someone/repo"}`,
		`{"name": "c", "repo": "file:///etc"}`,
		`{"name": "c", "repo": "ssh://git@example.com/x"}`,
		`{"name": "c", "repo": "https://r", "ref": "--upload-pack=x"}`,
	}
	for _, entry := range invalid {
		data := `[{"name": "Super-Team_1", "repo": "https://example.com/a.git"}, ` + entry + `]`
		teams, problems, err := ParseTeams([]byte(data), true)
		if err != nil || len(teams) != 1 || len(problems) != 1 {
			t.Errorf("expected %s to be left out with a problem: %+v, %v, %v", entry, teams, problems, err)
		}
	}

	// Local registries (staff) may use any repository
	if teams, problems, _ := ParseTeams([]byte(`[{"name": "c", "repo": "/srv/bot"}]`), false); len(teams) != 1 || len(problems) != 0 {
		t.Errorf("a local registry should accept local repositories: %v", problems)
	}

	if _, _, err := ParseTeams([]byte(`{`), true); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}

func TestLoadTeamsFromRepository(t *testing.T) {
	repo := newTestRepo(t)
	repo.commit(map[string]string{"teams.json": `[{"name": "a", "repo": "https://example.com/a"}, {"name": "b", "repo": "/tmp/b"}]`}, 10)
	dataDir := t.TempDir()

	teams, problems, err := LoadTeams(context.Background(), RegistryConfig{URL: repo.dir}, dataDir)
	if err != nil || len(teams) != 1 || teams[0].Name != "a" || len(problems) != 1 {
		t.Errorf("unexpected result: %+v, %v, %v", teams, problems, err)
	}

	// An invalid registry: the last valid version is used
	repo.commit(map[string]string{"teams.json": `[{"name": "a",`}, 20)
	teams, problems, err = LoadTeams(context.Background(), RegistryConfig{URL: repo.dir}, dataDir)
	if err != nil || len(teams) != 1 || teams[0].Name != "a" || len(problems) == 0 {
		t.Errorf("expected the last valid registry, with a problem: %+v, %v, %v", teams, problems, err)
	}

	// An unreachable registry: the last fetched version is used
	teams, problems, err = LoadTeams(context.Background(), RegistryConfig{URL: "/nonexistent"}, dataDir)
	if err != nil || len(teams) != 1 || len(problems) == 0 {
		t.Errorf("expected the last fetched registry, with a problem: %+v, %v, %v", teams, problems, err)
	}
}

func TestTeamID(t *testing.T) {
	// Free names are accepted, and the identifier is plain lowercase ASCII
	if err := validTeamName("👩\u200d🚀 🏳️\u200d🌈"); err != nil {
		t.Errorf("emoji sequences should be valid names: %v", err)
	}
	for _, name := range []string{"SuperTeam", "Les Abeilles 🐝", "Équipe Ruche", "蜂", "!!!", "a/../b", "-x", strings.Repeat("long", 16)} {
		id := TeamID(name)
		if !regexp.MustCompile(`^[a-z0-9]+$`).MatchString(id) {
			t.Errorf("the identifier of %q should be lowercase letters and digits: %q", name, id)
		}
	}
	if id := TeamID("Équipe Ruche"); !strings.HasPrefix(id, "equiperuche") {
		t.Errorf("accents should be removed: %q", id)
	}

	// Stable, and the same for names that only differ by case, spacing or normalization
	if TeamID("Super Team") != TeamID("  super   TEAM ") || TeamID("é") != TeamID("e\u0301") {
		t.Error("names that only differ by case, spacing or normalization should have the same identifier")
	}
	// Different names that look alike in ASCII are different teams
	for _, pair := range [][2]string{{"蜂", "蝶"}, {"a-b", "ab"}, {"a b", "ab"}, {"!!!", "???"}} {
		if TeamID(pair[0]) == TeamID(pair[1]) {
			t.Errorf("%q and %q should have different identifiers", pair[0], pair[1])
		}
	}

	// The identifier of an entry comes from its name, never from the file; the name is displayed as written
	teams, problems, err := ParseTeams([]byte(`[{"name": " Les Abeilles 🐝 ", "id": "root", "repo": "https://r"}]`), true)
	if err != nil || len(problems) != 0 || len(teams) != 1 || teams[0].Name != "Les Abeilles 🐝" || teams[0].ID != TeamID("Les Abeilles 🐝") {
		t.Errorf("unexpected result: %+v, %v, %v", teams, problems, err)
	}
}
