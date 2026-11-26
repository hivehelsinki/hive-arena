package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

// What the ladder is doing, for the status (empty: waiting)
var activity atomic.Value

func SetActivity(what string) { activity.Store(what) }

func Activity() string {
	what, _ := activity.Load().(string)
	return what
}

// Plays matches; implemented by Runner, and replaced in tests
type MatchPlayer interface {
	Run(ctx context.Context, match Match) (*MatchResult, error)
}

type Ladder struct {
	Config *Config
	// The ladder settings, reloaded before every match
	Settings *LiveSettings
	Store    *Store
	Player   MatchPlayer
	// Returns the current bot of each registered team, building new versions
	Pool func(ctx context.Context) ([]Bot, error)
	Rand *rand.Rand
	// Removes the containers and networks left by a previous process (nil:
	// none, as in tests, which must not touch the real ones)
	RemoveLeftovers func(ctx context.Context) error
	// The manual match, played first (nil: none)
	Manual *ManualGame
	// Builds the image of a version again if it is missing (nil: images are
	// assumed to be there)
	EnsureImage func(ctx context.Context, team Team, version TeamVersion) error
}

// After this many runner errors in a row (not bot failures), the
// matchmaker waits before trying again
const maxRunnerErrors = 3

// serverImageID returns the ID of the arena server image the matches use
func (l *Ladder) serverImageID(ctx context.Context) string {
	if l.Config.Runner.ServerImage == "" {
		return ""
	}
	id, err := docker(ctx, "image", "inspect", "--format", "{{.Id}}", l.Config.Runner.ServerImage)
	if err != nil {
		return ""
	}
	return id
}

// playMatch plays a match. A panic (a bug) fails the match, not the ladder.
func (l *Ladder) playMatch(ctx context.Context, match Match) (result *MatchResult, err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("Panic while playing a match: %v\n%s", p, debug.Stack())
			result, err = nil, fmt.Errorf("internal error: %v", p)
		}
	}()
	return l.Player.Run(ctx, match)
}

// teamNames returns the names of the teams of the bots, for people (the
// identifiers if the names cannot be read)
func (l *Ladder) teamNames(bots []Bot) string {
	names, err := l.Store.TeamNames()
	if err != nil {
		return botNames(bots)
	}
	var list []string
	for _, bot := range bots {
		list = append(list, teamName(names, bot.Name))
	}
	return strings.Join(list, ", ")
}

func botNames(bots []Bot) string {
	var names []string
	for _, bot := range bots {
		names = append(names, bot.Name)
	}
	return strings.Join(names, ", ")
}
