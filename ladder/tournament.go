package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// What the ladder is doing, for the status (empty: waiting)
var activity atomic.Value

func SetActivity(what string) { activity.Store(what) }

func Activity() string {
	what, _ := activity.Load().(string)
	return what
}

// Lineups returns every combination of n bots from the pool, in a stable order
func Lineups(pool []Bot, n int) [][]Bot {
	var lineups [][]Bot
	var current []Bot

	var pick func(start int)
	pick = func(start int) {
		if len(current) == n {
			lineups = append(lineups, append([]Bot{}, current...))
			return
		}
		for i := start; i <= len(pool)-(n-len(current)); i++ {
			current = append(current, pool[i])
			pick(i + 1)
			current = current[:len(current)-1]
		}
	}
	if n > 0 && n <= len(pool) {
		pick(0)
	}
	return lineups
}

// Above this number of lineups for a format, a run plays a random sample of
// them (with many teams, there are far more lineups than can be played)
const maxLineups = 5000

// PlanRun lists the matches of a run: for each format, every lineup of the
// pool, played `repeats` times. The order is shuffled, one repeat after the
// other, so that a run stopped at its deadline is still balanced: every
// lineup has been played the same number of times, give or take one.
func PlanRun(pool []Bot, formats []Format, repeats int, rng *rand.Rand) []Match {
	var round []Match
	for _, format := range formats {
		var lineups [][]Bot
		if binomial(len(pool), format.Players) > maxLineups {
			lineups = sampleLineups(pool, format.Players, maxLineups, rng)
			log.Printf("%s: too many lineups for %d bots, playing a random sample of %d", format, len(pool), maxLineups)
		} else {
			lineups = Lineups(pool, format.Players)
		}
		for _, lineup := range lineups {
			round = append(round, Match{Map: format.Map, Bots: lineup})
		}
	}

	var matches []Match
	for range repeats {
		shuffled := append([]Match{}, round...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		matches = append(matches, shuffled...)
	}
	return matches
}

// binomial returns n choose k, capped at a large value instead of overflowing
func binomial(n, k int) int {
	if k < 0 || k > n {
		return 0
	}
	result := 1
	for i := 1; i <= k; i++ {
		result = result * (n - k + i) / i
		if result > 1<<40 {
			return 1 << 40
		}
	}
	return result
}

// sampleLineups returns `count` distinct random lineups of n bots
func sampleLineups(pool []Bot, n int, count int, rng *rand.Rand) [][]Bot {
	seen := make(map[string]bool)
	var lineups [][]Bot
	for len(lineups) < count {
		indexes := rng.Perm(len(pool))[:n]
		slices.Sort(indexes)
		key := fmt.Sprint(indexes)
		if seen[key] {
			continue
		}
		seen[key] = true
		lineup := make([]Bot, n)
		for i, index := range indexes {
			lineup[i] = pool[index]
		}
		lineups = append(lineups, lineup)
	}
	return lineups
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
}

// After this many runner errors in a row (not bot failures), a run is
// aborted, and the matchmaker waits before trying again
const maxRunnerErrors = 3

// Play records and plays a run (a tournament): every lineup of the pool for
// each format, `repeats` times, until the deadline. `config` is recorded
// with the run.
func (l *Ladder) Play(ctx context.Context, kind string, config any, pool []Bot, formats []Format, repeats int, deadline time.Time) (int64, error) {
	matches := PlanRun(pool, formats, repeats, l.Rand)
	if len(matches) == 0 {
		return 0, fmt.Errorf("no matches to play with %d bots", len(pool))
	}

	runID, err := l.Store.CreateRun(kind, config, pool)
	if err != nil {
		return 0, err
	}
	log.Printf("Run %d (%s): %d bots, %d matches, deadline %s", runID, kind, len(pool), len(matches), deadline.Format(time.DateTime))
	info := MatchInfo{Kind: ManualMatch, RunID: runID, ServerImage: l.serverImageID(ctx), LadderRevision: gitRevision()}
	switch kind {
	case "ladder":
		info.Kind = LadderMatch
	case "tournament":
		info.Kind = TournamentMatch
	}
	if err := l.Store.SetRunVersions(runID, info.ServerImage, info.LadderRevision); err != nil {
		log.Printf("Run %d: could not record the versions: %s", runID, err)
	}
	defer SetActivity("")

	matchCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	status := RunComplete
	errors, recorded := 0, 0
	for i, match := range matches {
		if ctx.Err() != nil || time.Now().After(deadline) {
			log.Printf("Run %d: stopped after %d of %d matches", runID, i, len(matches))
			status = RunIncomplete
			break
		}

		SetActivity(fmt.Sprintf("run %d (%s): match %d of %d", runID, kind, i+1, len(matches)))
		log.Printf("Run %d: match %d/%d: %s, %s", runID, i+1, len(matches), match.Map, botNames(match.Bots))
		result, err := l.playMatch(matchCtx, match)
		if err != nil {
			log.Printf("Run %d: match %d failed: %s", runID, i+1, err)
			// Recorded as void, with the bots' output, unless the ladder is stopping
			if result != nil && ctx.Err() == nil {
				if id, err := l.Store.RecordMatch(info, result); err != nil {
					log.Printf("Run %d: could not record the failed match: %s", runID, err)
				} else {
					log.Printf("Run %d: failed match recorded as #%d (void)", runID, id)
				}
			}
			errors++
			if errors >= maxRunnerErrors {
				// The matches already played still count
				status = RunFailed
				if recorded > 0 {
					status = RunIncomplete
				}
				log.Printf("Run %d: stopped after %d failed matches in a row", runID, errors)
				break
			}
			continue
		}
		errors = 0

		id, err := l.Store.RecordMatch(info, result)
		result.RemoveReport()
		if err != nil {
			l.Store.FinishRun(runID, RunFailed)
			return runID, fmt.Errorf("recording a match: %w", err)
		}
		recorded++
		log.Printf("Run %d: match %d/%d recorded as #%d (%s)", runID, i+1, len(matches), id, result.Ended.Sub(result.Started).Round(time.Second))
	}

	if err := l.Store.FinishRun(runID, status); err != nil {
		return runID, err
	}
	log.Printf("Run %d: %s", runID, status)
	return runID, nil
}

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

func botNames(bots []Bot) string {
	var names []string
	for _, bot := range bots {
		names = append(names, bot.Name)
	}
	return strings.Join(names, ", ")
}
