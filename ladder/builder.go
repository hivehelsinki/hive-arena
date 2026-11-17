package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type BuildStatus string

const (
	BuildOK          BuildStatus = "ok"
	BuildFailed      BuildStatus = "build-failed"
	BuildSmokeFailed BuildStatus = "smoke-failed"
)

type Build struct {
	ID        int64       `json:"id"`
	Team      string      `json:"team"`
	Tag       string      `json:"tag"`
	Commit    string      `json:"commit"`
	Image     string      `json:"image"`
	Status    BuildStatus `json:"status"`
	StartedAt time.Time   `json:"startedAt"`
	EndedAt   time.Time   `json:"endedAt"`
	Error     string      `json:"error,omitempty"`
}

type Builder struct {
	Store   *Store
	DataDir string
	// Plays the smoke test match
	Smoke        MatchPlayer
	SmokeMap     string
	BuildTimeout time.Duration
	KeepImages   int // successful images kept per team
}

// The state of a team's bot, at the time of the last pool resolution
type TeamState struct {
	Team  Team   `json:"team"`
	Bot   *Bot   `json:"bot,omitempty"`   // the bot that plays, if any
	Error string `json:"error,omitempty"` // why the latest version is not the one that plays
}

func imageName(team string, commit string) string {
	return fmt.Sprintf("hive-ladder/%s:%s", strings.ToLower(team), commit[:12])
}

// Pool resolves the bot of each team: the latest tag on main (or the
// registered ref), built and smoke-tested if it is new. When that fails, the
// team's previous good build is used, if any.
func (b *Builder) Pool(ctx context.Context, teams []Team) ([]Bot, []TeamState) {
	var bots []Bot
	var states []TeamState
	for _, team := range teams {
		state := b.resolve(ctx, team)
		if state.Error != "" {
			log.Printf("Team %s: %s", team.Name, state.Error)
		}
		if state.Bot != nil {
			bots = append(bots, *state.Bot)
		}
		states = append(states, state)
	}
	return bots, states
}

func (b *Builder) resolve(ctx context.Context, team Team) TeamState {
	state := TeamState{Team: team}

	fallback := func(err error) TeamState {
		state.Error = err.Error()
		build, ferr := b.Store.LastGoodBuild(team.Name)
		if ferr == nil && build != nil {
			state.Bot = &Bot{Name: team.Name, Image: build.Image, Commit: build.Commit}
		}
		return state
	}

	dir := filepath.Join(b.DataDir, "repos", strings.ToLower(team.Name)+".git")
	if err := Mirror(ctx, team.Repo, dir); err != nil {
		return fallback(fmt.Errorf("fetching the repository: %w", err))
	}

	tag := Tag{Name: team.Ref}
	if team.Ref != "" {
		commit, err := ResolveRef(ctx, dir, team.Ref)
		if err != nil {
			return fallback(fmt.Errorf("resolving %s: %w", team.Ref, err))
		}
		tag.Commit = commit
	} else {
		latest, err := LatestTag(ctx, dir, "main")
		if err != nil {
			return fallback(err)
		}
		tag = *latest
	}

	build, err := b.Store.BuildFor(team.Name, tag.Commit)
	if err != nil {
		return fallback(err)
	}
	if build == nil {
		build = b.build(ctx, team, dir, tag)
	}
	if build.Status != BuildOK {
		return fallback(fmt.Errorf("%s (%s): %s", tag.Name, tag.Commit[:12], build.Status))
	}

	state.Bot = &Bot{Name: team.Name, Image: build.Image, Commit: build.Commit}
	return state
}

// build builds and smoke-tests a commit, and records the result
func (b *Builder) build(ctx context.Context, team Team, dir string, tag Tag) *Build {
	build := &Build{
		Team:      team.Name,
		Tag:       tag.Name,
		Commit:    tag.Commit,
		Image:     imageName(team.Name, tag.Commit),
		StartedAt: time.Now(),
	}
	log.Printf("Team %s: building %s (%s)", team.Name, tag.Name, tag.Commit[:12])

	var output strings.Builder
	err := b.dockerBuild(ctx, team, dir, build, &output)
	if err != nil {
		build.Status = BuildFailed
		build.Error = err.Error()
	} else {
		fmt.Fprintf(&output, "\n=== Smoke test: 1-player game on %s\n", b.SmokeMap)
		build.Status, build.Error = b.smokeTest(ctx, build, &output)
	}
	build.EndedAt = time.Now()

	if build.Status != BuildOK {
		docker(context.Background(), "rmi", build.Image)
	}

	id, err := b.Store.RecordBuild(build, output.String())
	if err != nil {
		log.Printf("Team %s: could not record the build: %s", team.Name, err)
	}
	build.ID = id
	log.Printf("Team %s: build %s: %s", team.Name, tag.Name, build.Status)

	if build.Status == BuildOK {
		b.prune(team.Name)
	}
	return build
}

// dockerBuild builds the image from the commit's files, without a checkout:
// git archive streams them to docker build as its context
func (b *Builder) dockerBuild(ctx context.Context, team Team, dir string, build *Build, output io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, b.BuildTimeout)
	defer cancel()

	tree := build.Commit
	if team.Context != "" {
		tree += ":" + strings.Trim(team.Context, "/")
	}
	dockerfile := team.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}

	archive := exec.CommandContext(ctx, "git", "archive", "--format=tar", tree)
	archive.Dir = dir
	stdout, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	archive.Stderr = output

	builder := exec.CommandContext(ctx, "docker", "build", "--progress=plain",
		"--label", DockerLabel+".team="+team.Name,
		"-t", build.Image, "-f", dockerfile, "-")
	builder.Stdin = stdout
	builder.Stdout = output
	builder.Stderr = output

	if err := archive.Start(); err != nil {
		return err
	}
	buildErr := builder.Run()
	archiveErr := archive.Wait()

	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("the build took more than %s", b.BuildTimeout)
	}
	if archiveErr != nil {
		return fmt.Errorf("reading the repository: %w", archiveErr)
	}
	if buildErr != nil {
		return fmt.Errorf("docker build failed")
	}
	return nil
}

// smokeTest plays a 1-player game with the new image: it must join and play until the end
func (b *Builder) smokeTest(ctx context.Context, build *Build, output io.Writer) (BuildStatus, string) {
	result, err := b.Smoke.Run(ctx, Match{Map: b.SmokeMap, Bots: []Bot{{Name: build.Team, Image: build.Image, Commit: build.Commit}}})
	if err != nil {
		fmt.Fprintf(output, "Error: %s\n", err)
		return BuildSmokeFailed, err.Error()
	}

	bot := result.Bots[0]
	fmt.Fprintf(output, "Status: %s, exit code %d, %d turns\n--- Agent output\n%s", bot.Status, bot.ExitCode, result.Result.Turns, bot.Log)
	if bot.Status != Finished {
		return BuildSmokeFailed, fmt.Sprintf("the agent did not play the game to the end (%s)", bot.Status)
	}
	return BuildOK, ""
}

// prune removes the images of a team's older successful builds
func (b *Builder) prune(team string) {
	builds, err := b.Store.GoodBuilds(team)
	if err != nil || len(builds) <= b.KeepImages {
		return
	}
	for _, build := range builds[b.KeepImages:] {
		docker(context.Background(), "rmi", build.Image)
	}
}
