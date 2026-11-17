package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// A duration that reads and writes as a string like "1m30s" in JSON
type Duration struct{ time.Duration }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	d.Duration = parsed
	return err
}

// A game format: a map and a number of players
type Format struct {
	Map     string `json:"map"`
	Players int    `json:"players"`
}

func (f Format) String() string {
	return fmt.Sprintf("%s (%d players)", f.Map, f.Players)
}

// The schedule of the ladder runs. It is published on the website as is.
type Schedule struct {
	Times    []string `json:"times"`    // daily run start times, "15:04"
	Timezone string   `json:"timezone"` // e.g. Europe/Helsinki
	Formats  []Format `json:"formats"`  // each run plays a full round-robin of each format
	Repeats  int      `json:"repeats"`  // times each lineup is played in a run
	Window   int      `json:"window"`   // number of recent runs the leaderboard is computed on
	// Bots that fail (crash or don't join) in this many matches in a row with
	// the same commit are left out of the next runs, until they change commit
	MaxFailures int `json:"maxFailures"`
}

type Config struct {
	DataDir  string   `json:"dataDir"`
	Schedule Schedule `json:"schedule"`
	// Margin between the deadline of a run and the start of the next one
	DeadlineMargin Duration `json:"deadlineMargin"`

	Runner struct {
		ServerImage  string   `json:"serverImage"`
		Cores        float64  `json:"cores"`
		Memory       string   `json:"memory"`
		Pids         int      `json:"pids"`
		JoinTimeout  Duration `json:"joinTimeout"`
		MatchTimeout Duration `json:"matchTimeout"`
	} `json:"runner"`

	// Where teams are registered
	Registry RegistryConfig `json:"registry"`

	Build struct {
		Timeout    Duration `json:"timeout"`
		KeepImages int      `json:"keepImages"` // successful images kept per team
		SmokeMap   string   `json:"smokeMap"`   // map of the 1-player smoke test
	} `json:"build"`

	Web struct {
		Listen string `json:"listen"` // address of the public website, e.g. ":8080"
		// Directory of the browser build of the viewer (see viewer/build-web.sh), served at /viewer/
		ViewerDir string `json:"viewerDir"`
	} `json:"web"`

	// Bots that always take part (e.g. the example agents), as references
	HouseBots []Bot `json:"houseBots"`
	// Additional bots given directly as images, outside of the registry
	Bots []Bot `json:"bots"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	defaults := DefaultRunnerConfig()
	config := &Config{
		DataDir:        "data",
		DeadlineMargin: Duration{10 * time.Minute},
		Schedule: Schedule{
			Times:       []string{"08:00", "16:00"},
			Timezone:    "Europe/Helsinki",
			Repeats:     1,
			Window:      4,
			MaxFailures: 5,
		},
	}
	config.Web.Listen = ":8080"
	config.Web.ViewerDir = "viewer/web"
	config.Build.Timeout = Duration{10 * time.Minute}
	config.Build.KeepImages = 3
	config.Build.SmokeMap = "tiny"
	config.Runner.ServerImage = defaults.ServerImage
	config.Runner.Cores = defaults.Cores
	config.Runner.Memory = defaults.Memory
	config.Runner.Pids = defaults.Pids
	config.Runner.JoinTimeout = Duration{defaults.JoinTimeout}
	config.Runner.MatchTimeout = Duration{defaults.MatchTimeout}

	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return config, config.validate()
}

func (c *Config) validate() error {
	if _, err := c.Location(); err != nil {
		return err
	}
	for _, t := range c.Schedule.Times {
		if _, err := time.Parse("15:04", t); err != nil {
			return fmt.Errorf("invalid run time %q", t)
		}
	}
	if len(c.Schedule.Formats) == 0 {
		return fmt.Errorf("no formats configured")
	}
	for _, f := range c.Schedule.Formats {
		if f.Players < 2 || f.Players > 6 {
			return fmt.Errorf("invalid format %s", f)
		}
	}
	if c.Schedule.Repeats < 1 || c.Schedule.Window < 1 {
		return fmt.Errorf("repeats and window must be at least 1")
	}
	names := make(map[string]bool)
	for _, bot := range append(append([]Bot{}, c.HouseBots...), c.Bots...) {
		if bot.Name == "" || bot.Image == "" || names[bot.Name] {
			return fmt.Errorf("bots need a unique name and an image: %+v", bot)
		}
		names[bot.Name] = true
	}
	return nil
}

func (c *Config) Location() (*time.Location, error) {
	return time.LoadLocation(c.Schedule.Timezone)
}

func (c *Config) RunnerConfig() RunnerConfig {
	config := DefaultRunnerConfig()
	config.ServerImage = c.Runner.ServerImage
	config.Cores = c.Runner.Cores
	config.Memory = c.Runner.Memory
	config.Pids = c.Runner.Pids
	config.JoinTimeout = c.Runner.JoinTimeout.Duration
	config.MatchTimeout = c.Runner.MatchTimeout.Duration
	return config
}

// NextRunTime returns the first scheduled run time strictly after `after`
func (s *Schedule) NextRunTime(after time.Time, loc *time.Location) time.Time {
	local := after.In(loc)
	var next time.Time
	for day := 0; day <= 1; day++ {
		for _, t := range s.Times {
			hm, _ := time.Parse("15:04", t)
			candidate := time.Date(local.Year(), local.Month(), local.Day()+day, hm.Hour(), hm.Minute(), 0, 0, loc)
			if candidate.After(after) && (next.IsZero() || candidate.Before(next)) {
				next = candidate
			}
		}
	}
	return next
}
