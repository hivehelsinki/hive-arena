package main

import (
	"math"
	"slices"
	"time"
)

// Ratings follow the Plackett-Luce model of OpenSkill (Weng and Lin, "A
// Bayesian Approximation Method for Online Ranking", JMLR 2011, algorithm 4),
// for free-for-all matches with ties, with TrueSkill's default parameters.

type RatingParams struct {
	Mu    float64 `json:"mu"`    // initial skill estimate
	Sigma float64 `json:"sigma"` // initial uncertainty
	Beta  float64 `json:"beta"`  // performance noise in a match
	Tau   float64 `json:"tau"`   // uncertainty added before every match, so that ratings follow changes
	// When a team's version changes, its uncertainty is raised to at least
	// this value, so that its rating follows a rewrite quickly (0: unchanged)
	VersionSigma float64 `json:"versionSigma"`
	// Teams are ranked by their score: mu - RankSigmas * sigma. The more
	// sigmas, the more a team has to prove itself (a new team starts low, and
	// a new version drops a team further); 0 ranks by the skill estimate alone.
	RankSigmas float64 `json:"rankSigmas"`
}

func DefaultRatingParams() RatingParams {
	return RatingParams{
		Mu:           25,
		Sigma:        25.0 / 3,
		Beta:         25.0 / 6,
		Tau:          25.0 / 300,
		VersionSigma: 25.0 / 6,
		RankSigmas:   2,
	}
}

// kappa keeps uncertainties positive
const kappa = 0.0001

type Rating struct {
	Mu    float64 `json:"mu"`
	Sigma float64 `json:"sigma"`
}

// Score is the conservative skill estimate teams are ranked by
func (p RatingParams) Score(r Rating) float64 {
	return r.Mu - p.RankSigmas*r.Sigma
}

// rate returns the ratings of the players of a match after it, given their
// ranks (lower is better, equal ranks are ties)
func (p RatingParams) rate(ratings []Rating, ranks []int) []Rating {
	n := len(ratings)
	if n < 2 {
		return slices.Clone(ratings)
	}

	sigma2 := make([]float64, n)
	sum := 0.0
	for i, r := range ratings {
		sigma2[i] = r.Sigma*r.Sigma + p.Tau*p.Tau
		sum += sigma2[i] + p.Beta*p.Beta
	}
	c := math.Sqrt(sum)

	strength := make([]float64, n)
	for i, r := range ratings {
		strength[i] = math.Exp(r.Mu / c)
	}
	// sumQ[q]: total strength of the players ranked q or worse; a[q]:
	// number of players tied with q
	sumQ := make([]float64, n)
	a := make([]float64, n)
	for q := range n {
		for i := range n {
			if ranks[i] >= ranks[q] {
				sumQ[q] += strength[i]
			}
			if ranks[i] == ranks[q] {
				a[q]++
			}
		}
	}

	result := make([]Rating, n)
	for i := range n {
		omega, delta := 0.0, 0.0
		for q := range n {
			if ranks[q] > ranks[i] {
				continue
			}
			share := strength[i] / sumQ[q]
			delta += share * (1 - share) / a[q]
			if q == i {
				omega += (1 - share) / a[q]
			} else {
				omega -= share / a[q]
			}
		}
		omega *= sigma2[i] / c
		gamma := math.Sqrt(sigma2[i]) / c
		delta *= gamma * sigma2[i] / (c * c)

		result[i] = Rating{
			Mu:    ratings[i].Mu + omega,
			Sigma: math.Sqrt(sigma2[i]) * math.Sqrt(max(1-delta, kappa)),
		}
	}
	return result
}

// One bot in a rated match
type RatedBot struct {
	Team   string
	Commit string
	Placement
}

type RatedMatch struct {
	ID     int64
	Time   time.Time
	Format string // see Format.Key
	Bots   []RatedBot
}

type TeamRating struct {
	Team   string  `json:"team"`
	Rating Rating  `json:"rating"`
	Score  float64 `json:"score"` // what teams are ranked by, see RatingParams.RankSigmas
	Games  int     `json:"games"`
	Commit string  `json:"commit"` // of its most recent match
	// When the team last played, and when its current version first played
	LastPlayed time.Time `json:"lastPlayed"`
	Since      time.Time `json:"since"`
}

// A rating table (a ladder): the ratings of the teams in a set of matches
type RatingTable struct {
	params RatingParams
	teams  map[string]*TeamRating
	// The ratings of each team after each of its matches, if recorded
	history map[string][]RatingPoint
}

// A team's rating after a match
type RatingPoint struct {
	Time   time.Time
	Rating Rating
	Score  float64
	Commit string
}

// RecordHistory makes the table keep the history of the ratings
func (l *RatingTable) RecordHistory() {
	l.history = make(map[string][]RatingPoint)
}

// History returns a team's ratings after each of its matches, if recorded
func (l *RatingTable) History(team string) []RatingPoint {
	return l.history[team]
}

func NewRatingTable(params RatingParams) *RatingTable {
	return &RatingTable{params: params, teams: make(map[string]*TeamRating)}
}

// Rating returns a team's rating, the initial one if it has not played
func (l *RatingTable) Rating(team string) TeamRating {
	if t, ok := l.teams[team]; ok {
		return *t
	}
	initial := Rating{l.params.Mu, l.params.Sigma}
	return TeamRating{Team: team, Rating: initial, Score: l.params.Score(initial)}
}

// Add rates a match
func (l *RatingTable) Add(match RatedMatch) {
	if len(match.Bots) < 2 {
		return
	}
	ratings := make([]Rating, len(match.Bots))
	placements := make([]Placement, len(match.Bots))
	for i, bot := range match.Bots {
		t := l.Rating(bot.Team)
		if t.Games > 0 && t.Commit != bot.Commit {
			t.Rating.Sigma = max(t.Rating.Sigma, l.params.VersionSigma)
		}
		ratings[i] = t.Rating
		placements[i] = bot.Placement
	}

	ranks, _ := matchRanks(placements)
	updated := l.params.rate(ratings, ranks)
	for i, bot := range match.Bots {
		t := l.Rating(bot.Team)
		if t.Games == 0 || t.Commit != bot.Commit {
			t.Since = match.Time
		}
		t.Rating = updated[i]
		t.Score = l.params.Score(t.Rating)
		t.Games++
		t.Commit = bot.Commit
		t.LastPlayed = match.Time
		l.teams[bot.Team] = &t
		if l.history != nil {
			l.history[bot.Team] = append(l.history[bot.Team], RatingPoint{match.Time, t.Rating, t.Score, t.Commit})
		}
	}
}

// Standings returns the teams that played, best first: by score, then name
func (l *RatingTable) Standings() []TeamRating {
	var standings []TeamRating
	for _, t := range l.teams {
		standings = append(standings, *t)
	}
	slices.SortFunc(standings, func(a, b TeamRating) int {
		if oa, ob := a.Score, b.Score; oa != ob {
			if oa > ob {
				return -1
			}
			return 1
		}
		if a.Team < b.Team {
			return -1
		}
		return 1
	})
	return standings
}

// RatingTables: the global ladder (all matches) and one per format
type RatingTables struct {
	Global    *RatingTable
	PerFormat map[string]*RatingTable
}

// ComputeRatings rates matches in the given order (chronological). The
// global table records the history of the ratings.
func ComputeRatings(params RatingParams, matches []RatedMatch) *RatingTables {
	ladders := &RatingTables{Global: NewRatingTable(params), PerFormat: make(map[string]*RatingTable)}
	ladders.Global.RecordHistory()
	for _, match := range matches {
		ladders.Add(match)
	}
	return ladders
}

func (l *RatingTables) Add(match RatedMatch) {
	l.Global.Add(match)
	format, ok := l.PerFormat[match.Format]
	if !ok {
		format = NewRatingTable(l.Global.params)
		l.PerFormat[match.Format] = format
	}
	format.Add(match)
}
