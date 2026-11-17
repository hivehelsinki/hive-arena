package main

import (
	"math"
	"slices"
)

// Points for first place. Every n - 1 in use (2, 3, 4 and 6 player games)
// divides 60 with a factor 2 to spare, so points are integers, ties included.
const MaxPoints = 60

// A bot's placement in a match, as needed for scoring
type Placement struct {
	Flowers uint
	Failed  bool // crashed or did not join
}

// MatchPoints returns the points of each bot in a match, in the same order.
// Bots are ordered by flowers, except that failed bots are all last, tied
// with each other. Tied bots share the average of the places they span.
func MatchPoints(placements []Placement) []int {
	n := len(placements)
	points := make([]int, n)
	if n < 2 {
		return points
	}

	// Sort keys: failed bots after all others, then more flowers first
	better := func(a, b Placement) bool {
		if a.Failed != b.Failed {
			return !a.Failed
		}
		if a.Failed {
			return false
		}
		return a.Flowers > b.Flowers
	}
	tied := func(a, b Placement) bool {
		return !better(a, b) && !better(b, a)
	}

	for i, p := range placements {
		// Places (1-based) spanned by p's group of tied bots
		first, size := 1, 0
		for _, other := range placements {
			if better(other, p) {
				first++
			} else if tied(other, p) {
				size++
			}
		}
		rank := float64(first) + float64(size-1)/2
		points[i] = int(math.Round(MaxPoints * (float64(n) - rank) / float64(n-1)))
	}
	return points
}

// Statistics of a series of match points
type Score struct {
	Games  int     `json:"games"`
	Mean   float64 `json:"mean"`
	StdErr float64 `json:"stdErr"` // standard error of the mean, 0 with fewer than 2 games
}

func NewScore(points []int) Score {
	n := len(points)
	if n == 0 {
		return Score{}
	}

	sum := 0.0
	for _, p := range points {
		sum += float64(p)
	}
	mean := sum / float64(n)

	score := Score{Games: n, Mean: mean}
	if n > 1 {
		variance := 0.0
		for _, p := range points {
			variance += (float64(p) - mean) * (float64(p) - mean)
		}
		variance /= float64(n - 1)
		score.StdErr = math.Sqrt(variance / float64(n))
	}
	return score
}

// One bot's result in one match, as stored
type BotResult struct {
	MatchID int64
	RunID   int64
	Map     string
	Players int
	Team    string
	Commit  string
	Points  int
	Status  BotStatus
}

type HeadToHead struct {
	Games int     `json:"games"`
	Wins  float64 `json:"wins"` // games where the first team finished ahead, ties count half
}

type Standing struct {
	Team   string           `json:"team"`
	Commit string           `json:"commit"` // of its most recent match
	Score  Score            `json:"score"`
	PerMap map[string]Score `json:"perMap"`
}

type Leaderboard struct {
	Standings []Standing `json:"standings"`
	// HeadToHead[a][b]: how often a finished ahead of b, in matches they both played
	HeadToHead map[string]map[string]HeadToHead `json:"headToHead"`
}

// NewLeaderboard computes standings and head-to-head results from bot
// results. Results must be ordered by match.
func NewLeaderboard(results []BotResult) Leaderboard {
	points := make(map[string][]int)
	perMap := make(map[string]map[string][]int)
	commits := make(map[string]string)
	h2h := make(map[string]map[string]HeadToHead)

	record := func(a, b BotResult) {
		if h2h[a.Team] == nil {
			h2h[a.Team] = make(map[string]HeadToHead)
		}
		entry := h2h[a.Team][b.Team]
		entry.Games++
		switch {
		case a.Points > b.Points:
			entry.Wins++
		case a.Points == b.Points:
			entry.Wins += 0.5
		}
		h2h[a.Team][b.Team] = entry
	}

	for start := 0; start < len(results); {
		end := start
		for end < len(results) && results[end].MatchID == results[start].MatchID {
			end++
		}
		match := results[start:end]

		for _, r := range match {
			points[r.Team] = append(points[r.Team], r.Points)
			if perMap[r.Team] == nil {
				perMap[r.Team] = make(map[string][]int)
			}
			perMap[r.Team][r.Map] = append(perMap[r.Team][r.Map], r.Points)
			commits[r.Team] = r.Commit

			for _, other := range match {
				if other.Team != r.Team {
					record(r, other)
				}
			}
		}
		start = end
	}

	var board Leaderboard
	board.HeadToHead = h2h
	for team, p := range points {
		standing := Standing{Team: team, Commit: commits[team], Score: NewScore(p), PerMap: make(map[string]Score)}
		for m, mp := range perMap[team] {
			standing.PerMap[m] = NewScore(mp)
		}
		board.Standings = append(board.Standings, standing)
	}

	slices.SortFunc(board.Standings, func(a, b Standing) int {
		if a.Score.Mean != b.Score.Mean {
			if a.Score.Mean > b.Score.Mean {
				return -1
			}
			return 1
		}
		if a.Team < b.Team {
			return -1
		}
		return 1
	})
	return board
}
