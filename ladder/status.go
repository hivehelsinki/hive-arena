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

func gitRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return ""
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

	CurrentRun *Run      `json:"currentRun,omitempty"`
	LastRun    *Run      `json:"lastRun,omitempty"`
	NextRun    time.Time `json:"nextRun"`

	Teams struct {
		Registered int      `json:"registered"`
		Playing    int      `json:"playing"`
		Errors     []string `json:"errors"` // teams whose latest version is not playing
	} `json:"teams"`
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

	if info, err := os.Stat(filepath.Join(web.Store.dir, "ladder.db")); err == nil {
		status.Database.Size = info.Size()
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
	if status.LastRun != nil && status.LastRun.Status == RunFailed {
		warn("the last run (#%d) failed", status.LastRun.ID)
	}
	if len(runs) > 0 && time.Since(runs[0].StartedAt) > maxTimeWithoutRun {
		warn("no run since %s: is the scheduler running?", runs[0].StartedAt.Format(time.DateTime))
	}

	if loc, err := web.Config.Location(); err == nil {
		status.NextRun = web.Config.Schedule.NextRunTime(time.Now(), loc)
	}

	states, _ := web.Store.TeamStates()
	status.Teams.Registered = len(states)
	status.Teams.Errors = []string{}
	for _, state := range states {
		if state.Bot != nil {
			status.Teams.Playing++
		}
		if state.Error != "" {
			status.Teams.Errors = append(status.Teams.Errors, state.Team.Name)
		}
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
