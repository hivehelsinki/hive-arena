package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// git runs a git command and returns its trimmed standard output
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		// Give up on transfers stalled below 1 kB/s for a minute
		"GIT_HTTP_LOW_SPEED_LIMIT=1000",
		"GIT_HTTP_LOW_SPEED_TIME=60",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// Mirror keeps a bare mirror of a remote repository up to date, in dir
func Mirror(ctx context.Context, url string, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		if _, err := git(ctx, dir, "remote", "set-url", "--", "origin", url); err != nil {
			return err
		}
		_, err := git(ctx, dir, "fetch", "--prune", "--prune-tags", "--force", "origin", "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*")
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	_, err := git(ctx, "", "clone", "--mirror", "--", url, dir)
	return err
}

type Tag struct {
	Name   string
	Commit string
}

// LatestTag returns the most recent tag (by commit date) among the tags that
// point to commits of the given branch, in a mirror. The tag name breaks ties.
func LatestTag(ctx context.Context, dir string, branch string) (*Tag, error) {
	// Commit dates: of the tagged commit (lightweight tags), or of the peeled
	// commit (annotated tags)
	out, err := git(ctx, dir, "for-each-ref", "--merged=refs/heads/"+branch,
		"--format=%(refname:short) %(objectname) %(committerdate:unix) %(*objectname) %(*committerdate:unix)", "refs/tags")
	if err != nil {
		return nil, err
	}

	type candidate struct {
		Tag
		time int64
	}
	var candidates []candidate
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		var tag Tag
		var date string
		switch len(fields) {
		case 3: // lightweight tag
			tag, date = Tag{Name: fields[0], Commit: fields[1]}, fields[2]
		case 4: // annotated tag (the tag object has no committer date)
			tag, date = Tag{Name: fields[0], Commit: fields[2]}, fields[3]
		case 5:
			tag, date = Tag{Name: fields[0], Commit: fields[3]}, fields[4]
		default:
			continue
		}
		t, _ := strconv.ParseInt(date, 10, 64)
		candidates = append(candidates, candidate{tag, t})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no tag on branch %s", branch)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].time != candidates[j].time {
			return candidates[i].time > candidates[j].time
		}
		return candidates[i].Name > candidates[j].Name
	})
	return &candidates[0].Tag, nil
}

// ResolveRef returns the commit a ref (tag, branch or commit) points to, in a mirror
func ResolveRef(ctx context.Context, dir string, ref string) (string, error) {
	return git(ctx, dir, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
}
