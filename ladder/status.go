package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

var processStart = time.Now()

// gitRevision returns the git commit the ladder was built from (with
// "-dirty" if the working tree had uncommitted changes), or "unknown" when
// not built with `go build` in a git repository (e.g. with `go run`)
func gitRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	revision, dirty := "", false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if revision == "" {
		return "unknown"
	}
	if dirty {
		revision += "-dirty"
	}
	return revision
}

const gigabyte = 1 << 30

// CheckDisk returns a warning if the data directory is running out of space, or ""
func CheckDisk(config *Config) string {
	free, _, err := diskSpace(config.DataDir)
	if err != nil {
		return "cannot check the free disk space: " + err.Error()
	}
	if float64(free) < config.MinFreeDiskGB*gigabyte {
		return fmt.Sprintf("low disk space: %.1f GB free in %s (warning below %.1f GB)", float64(free)/gigabyte, config.DataDir, config.MinFreeDiskGB)
	}
	return ""
}

// LogDiskWarning logs a warning if the data directory is running out of space
func LogDiskWarning(config *Config) {
	if warning := CheckDisk(config); warning != "" {
		log.Printf("WARNING: %s", warning)
	}
}

// Docker availability, checked at most every 30 seconds
type dockerCheck struct {
	mutex   sync.Mutex
	checked time.Time
	err     error
}

func (c *dockerCheck) get() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if time.Since(c.checked) > 30*time.Second {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, c.err = docker(ctx, "info", "--format", "{{.ServerVersion}}")
		cancel()
		c.checked = time.Now()
	}
	return c.err
}

type Status struct {
	Healthy  bool     `json:"healthy"`
	Warnings []string `json:"warnings"`

	Revision  string    `json:"revision"`
	StartedAt time.Time `json:"startedAt"`

	Disk struct {
		Free  uint64 `json:"free"`
		Total uint64 `json:"total"`
	} `json:"disk"`
	Docker   string `json:"docker"` // "ok", or the error
	Database struct {
		Size    int64 `json:"size"`
		Runs    int   `json:"runs"`
		Matches int   `json:"matches"`
		Builds  int   `json:"builds"`
	} `json:"database"`

	// What the ladder is doing (empty when waiting for the next run), and
	// why the last run could not be played, if it could not
	Activity     string `json:"activity"`
	LastRunError string `json:"lastRunError,omitempty"`

	CurrentRun *Run      `json:"currentRun,omitempty"`
	LastRun    *Run      `json:"lastRun,omitempty"`
	NextRun    time.Time `json:"nextRun"`

	Teams struct {
		Registered int       `json:"registered"`
		Playing    int       `json:"playing"`
		Errors     []string  `json:"errors"`  // teams whose latest version is not their current bot
		LeftOut    []string  `json:"leftOut"` // teams left out of the runs for failing
		UpdatedAt  time.Time `json:"updatedAt"`
	} `json:"teams"`
	// Problems found in the registry at the last resolution (invalid
	// entries, registry unreachable)
	RegistryProblems []string `json:"registryProblems"`
}

// A run longer ago than this means the scheduler is not running
const maxTimeWithoutRun = 25 * time.Hour

func (web *Web) status() Status {
	var status Status
	status.Revision = gitRevision()
	status.StartedAt = processStart
	warn := func(format string, args ...any) {
		status.Warnings = append(status.Warnings, fmt.Sprintf(format, args...))
	}

	if warning := CheckDisk(web.Config); warning != "" {
		warn("%s", warning)
	}
	status.Disk.Free, status.Disk.Total, _ = diskSpace(web.Config.DataDir)

	if err := web.docker.get(); err != nil {
		status.Docker = err.Error()
		warn("docker is not available: %s", err)
	} else {
		status.Docker = "ok"
	}

	for _, file := range []string{"ladder.db", "ladder.db-wal"} {
		if info, err := os.Stat(filepath.Join(web.Store.dir, file)); err == nil {
			status.Database.Size += info.Size()
		}
	}
	counts, err := web.Store.Counts()
	if err != nil {
		warn("cannot read the database: %s", err)
	}
	status.Database.Runs, status.Database.Matches, status.Database.Builds = counts[0], counts[1], counts[2]

	runs, err := web.Store.Runs("ladder", 2)
	if err == nil {
		for i := range runs {
			run := runs[i]
			run.Bots = nil
			if run.Status == RunRunning {
				status.CurrentRun = &run
			} else if status.LastRun == nil {
				status.LastRun = &run
			}
		}
	}
	status.Activity = Activity()
	if err, _ := lastRunError.Load().(string); err != "" {
		status.LastRunError = err
		warn("the last run could not be played: %s", err)
	}
	if status.LastRun != nil && status.LastRun.Status == RunFailed {
		warn("the last run (#%d) failed", status.LastRun.ID)
	}
	if len(runs) > 0 && time.Since(runs[0].StartedAt) > maxTimeWithoutRun {
		warn("no run since %s: is the scheduler running?", runs[0].StartedAt.Format(time.DateTime))
	}

	if loc, err := web.Config.Location(); err == nil {
		status.NextRun = web.Config.Schedule.NextRunTime(time.Now(), loc)
	}

	pool, _ := web.Store.PoolState()
	status.Teams.Registered = len(pool.Teams)
	status.Teams.UpdatedAt = pool.UpdatedAt
	status.Teams.Errors = []string{}
	status.Teams.LeftOut = []string{}
	for _, state := range pool.Teams {
		if state.Bot != nil && state.LeftOut == "" {
			status.Teams.Playing++
		}
		if state.Error != "" {
			status.Teams.Errors = append(status.Teams.Errors, state.Team.Name)
		}
		if state.LeftOut != "" {
			status.Teams.LeftOut = append(status.Teams.LeftOut, state.Team.Name)
		}
	}
	status.RegistryProblems = pool.Problems
	if status.RegistryProblems == nil {
		status.RegistryProblems = []string{}
	}

	if status.Warnings == nil {
		status.Warnings = []string{}
	}
	status.Healthy = len(status.Warnings) == 0
	return status
}

// GET /api/status: the state of the ladder, as JSON
func (web *Web) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, web.status())
}

// GET /health: 200 "ok", or 503 with the warnings, one per line, for monitoring
func (web *Web) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := web.status()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if !status.Healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, strings.Join(status.Warnings, "\n"))
		return
	}
	fmt.Fprintln(w, "ok")
}
