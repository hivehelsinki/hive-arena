package main

import (
	"slices"

	"encoding/json"
	"fmt"
	"github.com/hivehelsinki/hive-arena/arena"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
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

// Key identifies a format in the per-format scores
func (f Format) Key() string {
	return fmt.Sprintf("%s:%d", f.Map, f.Players)
}

// The settings of the ladder, re-read from the configuration file before
// every match, so that they can be tuned while the ladder runs
type LadderSettings struct {
	Formats []Format `json:"formats"`
	// Matches each team plays in each format per 24 hours (a rolling window).
	// Every match counts, whether the team challenged or was challenged.
	Quota int `json:"quota"`
	// Opponents are drawn among this many teams ranked just above the
	// challenger (at least players - 1)
	Window int `json:"window"`
	// Share of the matches whose opponents are drawn among all the teams
	RandomShare float64 `json:"randomShare"`
	// Bots that fail (crash or don't join) in this many matches in a row with
	// the same commit are left out, until they change commit (0: never)
	MaxFailures int `json:"maxFailures"`
	// Each team keeps the report and logs of its last matches (the results
	// are kept forever)
	KeepMatchFiles int `json:"keepMatchFiles"`
	// Head-to-head results are computed over this period
	HeadToHeadPeriod Duration `json:"headToHeadPeriod"`
	// No match is started while paused
	Paused bool         `json:"paused"`
	Rating RatingParams `json:"rating"`
}

func DefaultLadderSettings() LadderSettings {
	return LadderSettings{
		Quota:            20,
		Window:           4,
		RandomShare:      0.1,
		MaxFailures:      5,
		KeepMatchFiles:   100,
		HeadToHeadPeriod: Duration{7 * 24 * time.Hour},
		Rating:           DefaultRatingParams(),
	}
}

func (s *LadderSettings) validate(c *Config) error {
	if len(s.Formats) == 0 {
		return fmt.Errorf("no formats configured")
	}
	for _, f := range s.Formats {
		if f.Map != RandomMap {
			return fmt.Errorf("invalid format %s: the map must be %q (a new random map for each match)", f, RandomMap)
		}
		if f.Players < 2 || !slices.Contains(arena.RandomMapPlayers, f.Players) {
			return fmt.Errorf("invalid format %s: random maps are for %v players", f, arena.RandomMapPlayers)
		}
	}
	if s.Quota < 1 || s.Window < 1 || s.KeepMatchFiles < 1 || s.MaxFailures < 0 {
		return fmt.Errorf("ladder: quota, window and keepMatchFiles must be at least 1, maxFailures not negative")
	}
	if s.RandomShare < 0 || s.RandomShare > 1 {
		return fmt.Errorf("ladder: randomShare must be between 0 and 1")
	}
	if s.HeadToHeadPeriod.Duration <= 0 {
		return fmt.Errorf("ladder: headToHeadPeriod must be positive")
	}
	r := s.Rating
	if r.Sigma <= 0 || r.Beta <= 0 || r.Tau < 0 || r.VersionSigma < 0 {
		return fmt.Errorf("ladder.rating: sigma and beta must be positive, tau and versionSigma not negative")
	}
	return nil
}

type Config struct {
	DataDir string `json:"dataDir"`
	// A warning is logged, and /health reports a problem, below this free space
	MinFreeDiskGB float64 `json:"minFreeDiskGB"`

	// Time zone of the times shown on the website, e.g. Europe/Helsinki
	Timezone string `json:"timezone"`

	Ladder LadderSettings `json:"ladder"`

	Runner struct {
		ServerImage  string   `json:"serverImage"`
		CPUPerBot    float64  `json:"cpuPerBot"`
		Cores        float64  `json:"cores"`
		Memory       string   `json:"memory"`
		ServerMemory string   `json:"serverMemory"`
		Pids         int      `json:"pids"`
		JoinTimeout  Duration `json:"joinTimeout"`
		MatchTimeout Duration `json:"matchTimeout"`
	} `json:"runner"`

	// Where teams are registered
	Registry RegistryConfig `json:"registry"`

	Build struct {
		Timeout    Duration `json:"timeout"`
		KeepImages int      `json:"keepImages"` // successful images kept per team
		MaxImageMB int64    `json:"maxImageMB"` // larger images fail their build (0: no limit)
		CacheLimit string   `json:"cacheLimit"` // size the docker build cache is kept under, e.g. "10GB" (empty: no limit)
		// New versions are built this often, between matches
		Interval Duration `json:"interval"`
	} `json:"build"`

	Web struct {
		Listen string `json:"listen"` // address of the public website, e.g. ":8080"
		// Directory of the browser build of the viewer (see viewer/build-web.sh), served at /viewer/
		ViewerDir string `json:"viewerDir"`
		// Token for the backup route (/backup?token=), disabled if empty
		BackupToken string `json:"backupToken"`
	} `json:"web"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	defaults := DefaultRunnerConfig()
	config := &Config{
		DataDir:  "data",
		Timezone: "Europe/Helsinki",
		Ladder:   DefaultLadderSettings(),
	}
	config.MinFreeDiskGB = 5
	config.Web.Listen = ":8080"
	config.Web.ViewerDir = "viewer/web"
	config.Build.Timeout = Duration{10 * time.Minute}
	config.Build.KeepImages = 3
	config.Build.MaxImageMB = 2048
	config.Build.CacheLimit = "10GB"
	config.Build.Interval = Duration{15 * time.Minute}
	config.Runner.ServerImage = defaults.ServerImage
	config.Runner.CPUPerBot = defaults.CPUPerBot
	config.Runner.Cores = defaults.Cores
	config.Runner.Memory = defaults.Memory
	config.Runner.ServerMemory = defaults.ServerMemory
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
	if err := c.Ladder.validate(c); err != nil {
		return err
	}
	if c.Registry.URL == "" && c.Registry.Path == "" {
		return fmt.Errorf("no registry configured (registry.url or registry.path)")
	}

	r := c.Runner
	if r.ServerImage == "" || r.Memory == "" || r.ServerMemory == "" || r.Cores <= 0 || r.CPUPerBot <= 0 || r.Pids <= 0 || r.JoinTimeout.Duration <= 0 || r.MatchTimeout.Duration <= 0 {
		return fmt.Errorf("runner: serverImage and memory must be set, and cpuPerBot, cores, pids, joinTimeout and matchTimeout positive")
	}
	if c.Build.Timeout.Duration <= 0 || c.Build.KeepImages < 1 || c.Build.MaxImageMB < 0 || c.Build.Interval.Duration <= 0 {
		return fmt.Errorf("build: timeout and interval must be positive, and keepImages at least 1")
	}
	return nil
}

func (c *Config) Location() (*time.Location, error) {
	return time.LoadLocation(c.Timezone)
}

func (c *Config) RunnerConfig() RunnerConfig {
	config := DefaultRunnerConfig()
	config.ServerImage = c.Runner.ServerImage
	config.CPUPerBot = c.Runner.CPUPerBot
	config.Cores = c.Runner.Cores
	config.Memory = c.Runner.Memory
	config.ServerMemory = c.Runner.ServerMemory
	config.Pids = c.Runner.Pids
	config.JoinTimeout = c.Runner.JoinTimeout.Duration
	config.MatchTimeout = c.Runner.MatchTimeout.Duration
	return config
}

// RandomMap is the map name of a new random map for each match (see the
// arena server)
const RandomMap = "random"

// LiveSettings holds the ladder settings, reloaded from the configuration
// file when it changes. The other settings need a restart.
type LiveSettings struct {
	path     string
	mutex    sync.Mutex
	settings LadderSettings
	modTime  time.Time
	warning  string
}

// NewLiveSettings starts with the given settings, read from path (empty:
// never reloaded)
func NewLiveSettings(path string, settings LadderSettings) *LiveSettings {
	live := &LiveSettings{path: path, settings: settings}
	if info, err := os.Stat(path); path != "" && err == nil {
		live.modTime = info.ModTime()
	}
	return live
}

func (l *LiveSettings) Get() LadderSettings {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.settings
}

// Warning is why the configuration file could not be applied, or ""
func (l *LiveSettings) Warning() string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.warning
}

// Reload re-reads the configuration file if it changed, and returns whether
// the settings changed. An invalid file keeps the previous settings, with a
// warning.
func (l *LiveSettings) Reload() bool {
	if l.path == "" {
		return false
	}
	info, err := os.Stat(l.path)
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if err != nil {
		l.warning = fmt.Sprintf("cannot read the configuration: %s", err)
		return false
	}
	if info.ModTime().Equal(l.modTime) {
		return false
	}

	config, err := LoadConfig(l.path)
	if err != nil {
		l.warning = fmt.Sprintf("the configuration was not applied, the previous ladder settings are kept: %s", err)
		log.Printf("WARNING: %s", l.warning)
		return false
	}
	l.modTime = info.ModTime()
	l.warning = ""
	changed := !reflect.DeepEqual(config.Ladder, l.settings)
	l.settings = config.Ladder
	return changed
}
