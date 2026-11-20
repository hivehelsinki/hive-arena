package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"slices"
	"sync/atomic"
	"time"
)

// The matchmaker plays ladder matches one at a time, as long as some team
// has not played its quota in some format. Before every match, it reads the
// settings, the match history and the ratings: nothing else is kept, so a
// restart resumes where the ladder left off.

// When the matchmaker last looked for a match (zero if it does not run in
// this process), and why it could not play, if it could not
var heartbeat atomic.Value
var lastError atomic.Value

func Heartbeat() time.Time {
	t, _ := heartbeat.Load().(time.Time)
	return t
}

func LastError() string {
	err, _ := lastError.Load().(string)
	return err
}

func setLastError(err string) {
	lastError.Store(err)
	if err != "" {
		log.Printf("Matchmaker: %s", err)
	}
}

// How long the matchmaker waits when there is nothing to play, before
// looking again (new versions and settings changes are picked up then)
const idleWait = time.Minute

// After maxRunnerErrors failed matches in a row, the matchmaker waits this long
const errorWait = 5 * time.Minute

// Number of match files deleted at once when disk space is low
const lowDiskBatch = 20

// Serve plays ladder matches until the context is cancelled. A value on
// `wake` rebuilds the pool and looks for a match right away.
func (l *Ladder) Serve(ctx context.Context, wake <-chan struct{}) error {
	if l.RemoveLeftovers != nil {
		if err := l.RemoveLeftovers(ctx); err != nil {
			log.Printf("Could not remove leftover containers: %s", err)
		}
	}

	var pool []Bot
	var poolTime time.Time
	info := MatchInfo{LadderRevision: gitRevision()}
	errors := 0

	wait := func(d time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		case <-wake:
			log.Print("Matchmaker woken up: rebuilding the pool")
			poolTime = time.Time{}
		}
	}

	for ctx.Err() == nil {
		heartbeat.Store(time.Now())
		if l.Settings.Reload() {
			log.Printf("Ladder settings changed: %+v", l.Settings.Get())
		}
		settings := l.Settings.Get()

		// New versions, every build interval
		if time.Since(poolTime) >= l.Config.Build.Interval.Duration {
			SetActivity("building new versions")
			bots, err := l.Pool(ctx)
			if ctx.Err() != nil {
				break
			}
			if err != nil {
				// The previous pool keeps playing
				log.Printf("Building the pool: %s", err)
			} else {
				pool = bots
			}
			poolTime = time.Now()
			info.ServerImage = l.serverImageID(ctx)
			l.pruneFiles(settings)
		}
		l.freeDisk()

		if settings.Paused {
			SetActivity("paused")
			wait(idleWait)
			continue
		}

		match, idle, err := l.NextMatch(pool, settings, time.Now())
		if err != nil {
			setLastError(fmt.Sprintf("choosing a match: %s", err))
			SetActivity("")
			wait(idleWait)
			continue
		}
		if match == nil {
			SetActivity("idle: " + idle)
			wait(min(idleWait, time.Until(poolTime.Add(l.Config.Build.Interval.Duration))))
			continue
		}

		SetActivity(fmt.Sprintf("playing %s: %s", Format{match.Map, len(match.Bots)}, botNames(match.Bots)))
		log.Printf("Match: %s, %s", Format{match.Map, len(match.Bots)}, botNames(match.Bots))
		result, err := l.playMatch(ctx, *match)
		if ctx.Err() != nil {
			// Stopping: the match is lost, and not recorded
			if result != nil {
				result.RemoveReport()
			}
			break
		}
		if err != nil {
			log.Printf("Match failed: %s", err)
			errors++
		} else {
			errors = 0
		}
		if result != nil {
			// A failed match is recorded as void, with the bots' output
			id, err := l.Store.RecordMatch(info, result)
			result.RemoveReport()
			if err != nil {
				setLastError(fmt.Sprintf("recording a match: %s", err))
				wait(errorWait)
				continue
			}
			log.Printf("Match recorded as #%d (%s)", id, result.Ended.Sub(result.Started).Round(time.Second))
		}
		if errors >= maxRunnerErrors {
			setLastError(fmt.Sprintf("%d matches failed in a row (last: %s): waiting %s", errors, err, errorWait))
			SetActivity("waiting after failed matches")
			wait(errorWait)
			errors = 0
			continue
		}
		if errors == 0 {
			setLastError("")
		}
	}
	SetActivity("")
	return nil
}

// pruneFiles deletes the files of the matches no team keeps any more
func (l *Ladder) pruneFiles(settings LadderSettings) {
	if n, err := l.Store.PruneFiles(settings.KeepMatchFiles); err != nil {
		log.Printf("Deleting old match files: %s", err)
	} else if n > 0 {
		log.Printf("Deleted the report and logs of %d old matches", n)
	}
}

// freeDisk deletes the files of the oldest matches while disk space is low
func (l *Ladder) freeDisk() {
	for {
		free, _, err := diskSpace(l.Config.DataDir)
		if err != nil || float64(free) >= l.Config.MinFreeDiskGB*gigabyte {
			return
		}
		n, err := l.Store.DeleteOldestFiles(lowDiskBatch)
		if err != nil || n == 0 {
			return
		}
		log.Printf("Low disk space: deleted the report and logs of the %d oldest matches", n)
	}
}

// NextMatch chooses the next match to play, or returns nil and why there
// is nothing to play
func (l *Ladder) NextMatch(pool []Bot, settings LadderSettings, now time.Time) (*Match, string, error) {
	if len(pool) < 2 {
		return nil, fmt.Sprintf("%d teams can play", len(pool)), nil
	}
	commits := make(map[string]string)
	for _, bot := range pool {
		commits[bot.Name] = bot.Commit
	}
	counts, err := l.Store.MatchCounts(now.Add(-24*time.Hour), commits)
	if err != nil {
		return nil, "", err
	}
	matches, err := l.Store.RatedMatches(time.Time{})
	if err != nil {
		return nil, "", err
	}
	ratings := ComputeRatings(settings.Rating, matches)
	match := pickMatch(pool, settings, counts, ratings, l.Rand)
	if match == nil {
		return nil, "every team has played its quota of the last 24 hours", nil
	}
	return match, "", nil
}

// pickMatch chooses the next match: the challenger is the team furthest
// below its quota in some format, its opponents are drawn among the teams
// ranked just above it in that format (or, for a share of the matches,
// among all teams). Returns nil if every quota is met.
func pickMatch(pool []Bot, settings LadderSettings, counts map[string]map[string]int, ratings *RatingTables, rng *rand.Rand) *Match {
	type candidate struct {
		bot    Bot
		format Format
	}
	var best []candidate
	bestDeficit := 0
	var seen []string
	for _, format := range settings.Formats {
		if format.Players > len(pool) || slices.Contains(seen, format.Key()) {
			continue
		}
		seen = append(seen, format.Key())
		for _, bot := range pool {
			deficit := settings.Quota - counts[bot.Name][format.Key()]
			if deficit > bestDeficit {
				best, bestDeficit = nil, deficit
			}
			if deficit == bestDeficit && deficit > 0 {
				best = append(best, candidate{bot, format})
			}
		}
	}
	if len(best) == 0 {
		return nil
	}
	challenger := best[rng.Intn(len(best))]
	format := challenger.format
	n := format.Players

	// The format's ladder, best first
	table := ratings.PerFormat[format.Key()]
	if table == nil {
		table = NewRatingTable(settings.Rating)
	}
	ladder := slices.Clone(pool)
	slices.SortFunc(ladder, func(a, b Bot) int {
		oa, ob := table.Rating(a.Name).Score, table.Rating(b.Name).Score
		if oa != ob {
			if oa > ob {
				return -1
			}
			return 1
		}
		if a.Name < b.Name {
			return -1
		}
		return 1
	})
	index := slices.IndexFunc(ladder, func(b Bot) bool { return b.Name == challenger.bot.Name })

	var candidates []Bot
	if rng.Float64() < settings.RandomShare {
		candidates = append(slices.Clone(ladder[:index]), ladder[index+1:]...)
	} else {
		// The teams just above, and below when there are not enough above
		window := min(max(settings.Window, n-1), len(ladder)-1)
		above := ladder[max(0, index-window):index]
		below := ladder[index+1 : index+1+window-len(above)]
		candidates = append(slices.Clone(above), below...)
	}

	bots := []Bot{challenger.bot}
	for _, i := range rng.Perm(len(candidates))[:n-1] {
		bots = append(bots, candidates[i])
	}
	rng.Shuffle(len(bots), func(i, j int) { bots[i], bots[j] = bots[j], bots[i] })
	return &Match{Map: format.Map, Bots: bots}
}
