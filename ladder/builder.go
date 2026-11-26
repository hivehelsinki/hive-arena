package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"strconv"
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
	BuildTimeout time.Duration
	KeepImages   int   // successful images kept per team (at least 1)
	MaxImageSize int64 // in bytes, 0 for no limit

	// For the service only, as they affect all of the machine's builds:
	// clear the build cache mounts before each build (they are shared
	// between all builds, so a team could plant files in another team's
	// build), and limit the build cache to this size (e.g. "10GB")
	IsolateCaches bool
	CacheLimit    string
}

// Time limit for fetching a repository
const fetchTimeout = 5 * time.Minute

// errNotRecorded is returned by build when it could not be completed for a
// reason that is not the team's (interruption, docker or network failure):
// it is not recorded, and tried again at the next resolution
type errNotRecorded struct{ error }

// The state of a team's bot, at the time of the last pool resolution
type TeamState struct {
	Team    Team   `json:"team"`
	Bot     *Bot   `json:"bot,omitempty"`     // the team's current bot, if any
	Error   string `json:"error,omitempty"`   // why the latest version is not the current bot
	LeftOut string `json:"leftOut,omitempty"` // why the current bot does not play
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
			log.Printf("Team %s: %s", team.ID, state.Error)
		}
		if state.Bot != nil {
			bots = append(bots, *state.Bot)
		}
		states = append(states, state)
	}
	if b.CacheLimit != "" {
		b.limitCache()
	}
	return bots, states
}

// imageExists reports whether a docker image is present. When docker does
// not answer, the image is assumed to be there.
func imageExists(image string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err := docker(ctx, "image", "inspect", "--format", "{{.Id}}", image)
	if err == nil {
		return true
	}
	return !strings.Contains(err.Error(), "No such image")
}

func (b *Builder) resolve(ctx context.Context, team Team) TeamState {
	state := TeamState{Team: team}

	// The previous good build whose image is still there
	fallback := func(err error) TeamState {
		state.Error = err.Error()
		builds, ferr := b.Store.GoodBuilds(team.ID)
		if ferr != nil {
			return state
		}
		for _, build := range builds {
			if imageExists(build.Image) {
				state.Bot = &Bot{Name: team.ID, Image: build.Image, Commit: build.Commit}
				break
			}
		}
		return state
	}

	dir := filepath.Join(b.DataDir, "repos", team.ID+".git")
	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	err := Mirror(fetchCtx, team.Repo, dir)
	cancel()
	if err != nil {
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

	build, err := b.Store.BuildFor(team.ID, tag.Commit)
	if err != nil {
		return fallback(err)
	}
	// A good build whose image disappeared (pruned, or restored data on
	// another machine) is built again
	if build != nil && build.Status == BuildOK && !imageExists(build.Image) {
		log.Printf("Team %s: the image %s is missing, building it again", team.ID, build.Image)
		build = nil
	}
	if build == nil {
		build, err = b.build(ctx, team, dir, tag)
		if err != nil {
			return fallback(fmt.Errorf("%s (%s): %w; it will be tried again", tag.Name, tag.Commit[:12], err))
		}
	}
	if build.Status != BuildOK {
		return fallback(fmt.Errorf("%s (%s): %s", tag.Name, tag.Commit[:12], build.Status))
	}

	state.Bot = &Bot{Name: team.ID, Image: build.Image, Commit: build.Commit}
	return state
}

// build builds and smoke-tests a commit, and records the result. Failures
// that are not the team's are not recorded: they return an error.
func (b *Builder) build(ctx context.Context, team Team, dir string, tag Tag) (*Build, error) {
	build := &Build{
		Team:      team.ID,
		Tag:       tag.Name,
		Commit:    tag.Commit,
		Image:     imageName(team.ID, tag.Commit),
		StartedAt: time.Now(),
	}
	log.Printf("Team %s: building %s (%s)", team.ID, tag.Name, tag.Commit[:12])

	if b.IsolateCaches {
		b.clearCacheMounts()
	}

	var output strings.Builder
	err := b.dockerBuild(ctx, team, dir, build, &output)
	if err != nil && transientBuildFailure(output.String()) {
		err = errNotRecorded{fmt.Errorf("%w (registry or network failure)", err)}
	}
	if err == nil {
		err = b.checkImageSize(build.Image)
	}
	if err != nil {
		build.Status = BuildFailed
		build.Error = err.Error()
	} else {
		fmt.Fprintf(&output, "\n=== Smoke test: 1-player game on a random map\n")
		build.Status, build.Error, err = b.smokeTest(ctx, build, &output)
	}
	build.EndedAt = time.Now()

	if build.Status != BuildOK || err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		docker(cleanup, "rmi", build.Image)
		cancel()
	}

	var notRecorded errNotRecorded
	if errors.As(err, &notRecorded) || ctx.Err() != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("interrupted")
		}
		log.Printf("Team %s: build %s not completed: %s", team.ID, tag.Name, err)
		return nil, err
	}

	id, err := b.Store.RecordBuild(build, output.String())
	if err != nil {
		log.Printf("Team %s: could not record the build: %s", team.ID, err)
	}
	build.ID = id
	log.Printf("Team %s: build %s: %s", team.ID, tag.Name, build.Status)

	if build.Status == BuildOK {
		b.prune(team.ID)
	}
	return build, nil
}

// transientBuildFailure reports whether a build failed because of a
// temporary problem of the image registry, according to its output
func transientBuildFailure(output string) bool {
	for _, sign := range []string{"toomanyrequests", "TLS handshake timeout", "503 Service Unavailable"} {
		if strings.Contains(output, sign) {
			return true
		}
	}
	return false
}

// checkImageSize fails if an image is larger than the limit
func (b *Builder) checkImageSize(image string) error {
	if b.MaxImageSize <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := docker(ctx, "image", "inspect", "--format", "{{.Size}}", image)
	if err != nil {
		return errNotRecorded{err}
	}
	size, _ := strconv.ParseInt(out, 10, 64)
	if size > b.MaxImageSize {
		return fmt.Errorf("the image is too large: %d MB, the limit is %d MB", size>>20, b.MaxImageSize>>20)
	}
	return nil
}

// clearCacheMounts removes the contents of build cache mounts
// (RUN --mount=type=cache), which are shared between all builds
func (b *Builder) clearCacheMounts() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := docker(ctx, "builder", "prune", "--force", "--filter", "type=exec.cachemount"); err != nil {
		log.Printf("Could not clear the build cache mounts: %s", err)
	}
}

// limitCache keeps the build cache under its size limit
func (b *Builder) limitCache() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_, err := docker(ctx, "builder", "prune", "--force", "--max-used-space", b.CacheLimit)
	if err != nil && strings.Contains(err.Error(), "unknown flag") {
		// Docker before 28
		_, err = docker(ctx, "builder", "prune", "--force", "--keep-storage", b.CacheLimit)
	}
	if err != nil {
		log.Printf("Could not limit the build cache: %s", err)
	}
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
	dockerfile, err := contextDockerfile(team)
	if err != nil {
		return err
	}

	archive := exec.CommandContext(ctx, "git", "archive", "--format=tar", tree)
	archive.Dir = dir
	stdout, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	archive.Stderr = output

	builder := exec.CommandContext(ctx, "docker", "build", "--progress=plain",
		"--label", DockerLabel+".team="+team.ID,
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
		return errNotRecorded{fmt.Errorf("reading the repository: %w", archiveErr)}
	}
	if buildErr != nil {
		if !dockerAvailable() {
			return errNotRecorded{fmt.Errorf("docker is not available")}
		}
		return fmt.Errorf("docker build failed")
	}
	return nil
}

// smokeTest plays a 1-player game with the new image: it must join and play
// until the end. A match that could not be played at all (server or docker
// failure) is not the team's failure: it returns an error.
func (b *Builder) smokeTest(ctx context.Context, build *Build, output io.Writer) (BuildStatus, string, error) {
	result, err := b.Smoke.Run(ctx, Match{Map: RandomMap, Bots: []Bot{{Name: build.Team, Image: build.Image, Commit: build.Commit}}})
	result.RemoveReport()
	if err != nil {
		fmt.Fprintf(output, "Error: %s\n", err)
		if result == nil {
			return "", "", errNotRecorded{fmt.Errorf("smoke test: %w", err)}
		}
		fmt.Fprintf(output, "--- Agent output\n%s", result.Bots[0].Log)
		return BuildSmokeFailed, err.Error(), nil
	}

	bot := result.Bots[0]
	fmt.Fprintf(output, "Status: %s, exit code %d, %d turns\n--- Agent output\n%s", bot.Status, bot.ExitCode, result.Result.Turns, bot.Log)
	if bot.Status != Finished {
		return BuildSmokeFailed, fmt.Sprintf("the agent did not play the game to the end (%s)", bot.Status), nil
	}
	return BuildOK, "", nil
}

// prune removes the images of a team's older successful builds
func (b *Builder) prune(team string) {
	builds, err := b.Store.GoodBuilds(team)
	if err != nil || len(builds) <= b.KeepImages {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// Older builds may share their image with a kept one (a rebuilt image)
	keep := max(b.KeepImages, 1)
	kept := make(map[string]bool)
	for _, build := range builds[:keep] {
		kept[build.Image] = true
	}
	for _, build := range builds[keep:] {
		if !kept[build.Image] {
			docker(ctx, "rmi", build.Image)
		}
	}
}

// contextDockerfile returns the path of a team's Dockerfile inside its build
// context. Both are given relative to the repository root; the default is the
// Dockerfile at the top of the context.
func contextDockerfile(team Team) (string, error) {
	if team.Dockerfile == "" {
		return "Dockerfile", nil
	}
	context := strings.Trim(team.Context, "/")
	dockerfile := strings.Trim(team.Dockerfile, "/")
	if context == "" {
		return dockerfile, nil
	}
	relative, found := strings.CutPrefix(dockerfile, context+"/")
	if !found {
		return "", fmt.Errorf("the Dockerfile %s is not inside the build context %s", team.Dockerfile, team.Context)
	}
	return relative, nil
}
