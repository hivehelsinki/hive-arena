package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Files of the Lua example agent, to commit in test repositories
func luaAgentFiles(t *testing.T) map[string]string {
	files := make(map[string]string)
	paths, _ := filepath.Glob("../example-agent-lua/*")
	for _, path := range paths {
		if strings.HasSuffix(path, ".md") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(path)] = string(data)
	}
	return files
}

func TestBuilder(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	store := newTestStore(t)
	smoke := DefaultRunnerConfig()
	smoke.MatchTimeout = 2 * time.Minute
	builder := &Builder{
		Store:        store,
		DataDir:      t.TempDir(),
		Smoke:        &Runner{Config: smoke},
		SmokeMap:     "tiny",
		BuildTimeout: 5 * time.Minute,
		KeepImages:   1,
	}

	repo := newTestRepo(t)
	team := Team{Name: "Testers", Repo: repo.dir}

	// A working agent
	good := repo.commit(luaAgentFiles(t), 10)
	repo.git("tag", "v1", good)

	bots, states := builder.Pool(ctx, []Team{team})
	if len(bots) != 1 || bots[0].Commit != good || bots[0].Image != imageName("Testers", good) || states[0].Error != "" {
		t.Fatalf("expected the v1 bot, got %+v %+v", bots, states)
	}
	build, _ := store.BuildFor("Testers", good)
	if build == nil || build.Status != BuildOK || build.Tag != "v1" {
		t.Fatalf("unexpected build: %+v", build)
	}
	log, _ := readGzip(store.BuildLogPath(build.ID))
	if !strings.Contains(string(log), "Smoke test") {
		t.Errorf("the build log should include the smoke test:\n%s", log)
	}

	// A broken Dockerfile: the previous version keeps playing
	broken := repo.commit(map[string]string{"Dockerfile": "FROM busybox\nRUN exit 1\n"}, 20)
	repo.git("tag", "v2", broken)

	bots, states = builder.Pool(ctx, []Team{team})
	if len(bots) != 1 || bots[0].Commit != good || !strings.Contains(states[0].Error, "build-failed") {
		t.Fatalf("expected the v1 bot as a fallback, got %+v %+v", bots, states)
	}

	// An image that builds, but crashes: fails the smoke test
	crash := repo.commit(map[string]string{"Dockerfile": "FROM busybox\nENTRYPOINT [\"false\"]\n"}, 30)
	repo.git("tag", "v3", crash)

	bots, states = builder.Pool(ctx, []Team{team})
	if len(bots) != 1 || bots[0].Commit != good || !strings.Contains(states[0].Error, "smoke-failed") {
		t.Fatalf("expected the v1 bot as a fallback, got %+v %+v", bots, states)
	}

	// Failed builds are not retried
	before, _ := store.Builds("Testers", 100)
	builder.Pool(ctx, []Team{team})
	after, _ := store.Builds("Testers", 100)
	if len(after) != len(before) {
		t.Errorf("a failed build should not be retried: %d builds, then %d", len(before), len(after))
	}

	// A team without any good build does not play
	other := newTestRepo(t)
	other.commit(map[string]string{"Dockerfile": "FROM busybox\nRUN exit 1\n"}, 10)
	other.git("tag", "v1")
	bots, _ = builder.Pool(ctx, []Team{{Name: "other", Repo: other.dir}})
	if len(bots) != 0 {
		t.Errorf("expected no bot, got %+v", bots)
	}

	// Unreachable repository
	bots, states = builder.Pool(ctx, []Team{{Name: "Testers", Repo: "/nonexistent"}})
	if len(bots) != 1 || bots[0].Commit != good || states[0].Error == "" {
		t.Errorf("expected the last good build as a fallback, got %+v %+v", bots, states)
	}
}

func TestContextDockerfile(t *testing.T) {
	cases := []struct {
		dockerfile, context, want string
		fails                     bool
	}{
		{"", "", "Dockerfile", false},
		{"", "bot", "Dockerfile", false},
		{"build/Dockerfile", "", "build/Dockerfile", false},
		{"bot/Dockerfile", "bot", "Dockerfile", false},
		{"bot/docker/Dockerfile", "bot/", "docker/Dockerfile", false},
		{"Dockerfile", "bot", "", true},
		{"bots/Dockerfile", "bot", "", true},
	}
	for _, c := range cases {
		got, err := contextDockerfile(Team{Dockerfile: c.dockerfile, Context: c.context})
		if (err != nil) != c.fails || got != c.want {
			t.Errorf("dockerfile %q, context %q: got %q, %v", c.dockerfile, c.context, got, err)
		}
	}
}
