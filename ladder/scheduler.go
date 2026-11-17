package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"
)

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

// PlanRun lists the matches of a run: for each format, every lineup of the
// pool, played `repeats` times. The order is shuffled, one repeat after the
// other, so that a run stopped at its deadline is still balanced: every
// lineup has been played the same number of times, give or take one.
func PlanRun(pool []Bot, formats []Format, repeats int, rng *rand.Rand) []Match {
	var round []Match
	for _, format := range formats {
		for _, lineup := range Lineups(pool, format.Players) {
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

// Plays matches; implemented by Runner, and replaced in tests
type MatchPlayer interface {
	Run(ctx context.Context, match Match) (*MatchResult, error)
}

type Ladder struct {
	Config *Config
	Store  *Store
	Player MatchPlayer
	// Returns the current bot of each registered team
	Pool func(ctx context.Context) ([]Bot, error)
	Rand *rand.Rand
}

// After this many runner errors in a row (not bot failures), a run is aborted
const maxRunnerErrors = 3

// RunOnce plays a complete ladder run, until its deadline
func (l *Ladder) RunOnce(ctx context.Context, deadline time.Time) (int64, error) {
	teams, err := l.Pool(ctx)
	if err != nil {
		return 0, fmt.Errorf("getting the pool: %w", err)
	}

	var pool []Bot
	for _, bot := range teams {
		failures, err := l.Store.ConsecutiveFailures(bot.Name, bot.Commit)
		if err != nil {
			return 0, err
		}
		if l.Config.Schedule.MaxFailures > 0 && failures >= l.Config.Schedule.MaxFailures {
			log.Printf("Leaving %s out of the run: failed %d matches in a row with commit %s", bot.Name, failures, bot.Commit)
			continue
		}
		pool = append(pool, bot)
	}

	return l.Play(ctx, "ladder", l.Config.Schedule, pool, l.Config.Schedule.Formats, l.Config.Schedule.Repeats, deadline)
}

// Play records and plays a run: every lineup of the pool for each format,
// `repeats` times, until the deadline. `config` is recorded with the run.
func (l *Ladder) Play(ctx context.Context, kind string, config any, pool []Bot, formats []Format, repeats int, deadline time.Time) (int64, error) {
	runID, err := l.Store.CreateRun(kind, config, pool)
	if err != nil {
		return 0, err
	}

	matches := PlanRun(pool, formats, repeats, l.Rand)
	log.Printf("Run %d (%s): %d bots, %d matches, deadline %s", runID, kind, len(pool), len(matches), deadline.Format(time.DateTime))

	status := RunComplete
	errors := 0
	for i, match := range matches {
		if ctx.Err() != nil || time.Now().After(deadline) {
			log.Printf("Run %d: stopped after %d of %d matches", runID, i, len(matches))
			status = RunIncomplete
			break
		}

		result, err := l.Player.Run(ctx, match)
		if err != nil {
			log.Printf("Run %d: match %d (%s) failed: %s", runID, i+1, match.Map, err)
			errors++
			if errors >= maxRunnerErrors {
				status = RunFailed
				break
			}
			continue
		}
		errors = 0

		id, err := l.Store.RecordMatch(runID, result)
		if err != nil {
			l.Store.FinishRun(runID, RunFailed)
			return runID, fmt.Errorf("recording a match: %w", err)
		}
		log.Printf("Run %d: match %d/%d recorded as #%d (%s, %s)", runID, i+1, len(matches), id, match.Map, result.Ended.Sub(result.Started).Round(time.Second))
	}

	if err := l.Store.FinishRun(runID, status); err != nil {
		return runID, err
	}
	log.Printf("Run %d: %s", runID, status)
	return runID, nil
}

// Serve runs the ladder at the scheduled times, and when `trigger` receives
// a value, until the context is cancelled
func (l *Ladder) Serve(ctx context.Context, trigger <-chan struct{}) error {
	loc, err := l.Config.Location()
	if err != nil {
		return err
	}
	if err := l.Store.MarkInterruptedRuns(); err != nil {
		return err
	}

	for {
		next := l.Config.Schedule.NextRunTime(time.Now(), loc)
		log.Printf("Next run at %s", next.Format(time.DateTime))

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Until(next)):
		case <-trigger:
			log.Print("Run triggered manually")
		}

		// The run must end before the one after it
		deadline := l.Config.Schedule.NextRunTime(time.Now(), loc).Add(-l.Config.DeadlineMargin.Duration)
		if _, err := l.RunOnce(ctx, deadline); err != nil {
			log.Printf("Run failed: %s", err)
		}
	}
}
