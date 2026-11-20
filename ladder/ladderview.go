package main

import (
	"math"
	"slices"
	"time"
)

// A team in the ladder, as shown on the website
type LadderRow struct {
	Team   string     `json:"team"`
	Global TeamRating `json:"global"`
	// Ratings in each format the team played (see Format.Key)
	PerFormat map[string]TeamRating `json:"perFormat"`
	// Whether the team currently plays (a working version, not left out)
	Playing bool `json:"playing"`
}

type FormatTable struct {
	Format Format       `json:"format"`
	Teams  []TeamRating `json:"teams"` // best first
}

type LadderView struct {
	Rows    []LadderRow   `json:"ladder"`  // the global ladder, best first
	Formats []Format      `json:"formats"` // the configured formats
	Tables  []FormatTable `json:"perFormat"`
	// HeadToHead[a][b]: how often a finished ahead of b, in the matches they
	// played together over the period
	HeadToHead       map[string]map[string]HeadToHead `json:"headToHead"`
	HeadToHeadPeriod Duration                         `json:"headToHeadPeriod"`
	Teams            []string                         `json:"-"` // in ladder order, for the head-to-head table
	Rated            int                              `json:"ratedMatches"`
	// The global rating of each team over time, in ladder order, for charts
	History []TeamHistory `json:"history"`
}

// A team's global rating over time, reduced to a chart's resolution
type TeamHistory struct {
	Team   string       `json:"team"`
	Points []ChartPoint `json:"points"`
}

type ChartPoint struct {
	Time    int64   `json:"t"`           // Unix seconds
	Rating  float64 `json:"r"`           // the ordinal, rounded
	Version string  `json:"v,omitempty"` // "tag (revision)", where a new version starts
}

// Points of a team's history kept for charts: about one per pixel
const chartPoints = 400

// chartHistory reduces a history to at most about `max` points: the last
// one of each time slot, the first one, and the first match of each version
func chartHistory(history []RatingPoint, from time.Time, to time.Time, max int, label func(commit string) string) []ChartPoint {
	slot := to.Sub(from) / time.Duration(max)
	var points []ChartPoint
	lastCommit := ""
	for i, p := range history {
		point := ChartPoint{Time: p.Time.Unix(), Rating: math.Round(p.Score*100) / 100}
		newVersion := p.Commit != lastCommit
		if newVersion {
			point.Version = label(p.Commit)
			lastCommit = p.Commit
		}
		// A point in the same slot as the previous one replaces it, unless
		// one of them starts a version
		if n := len(points); n > 0 && !newVersion && points[n-1].Version == "" && i > 0 && slot > 0 &&
			p.Time.Sub(from)/slot == time.Unix(points[n-1].Time, 0).Sub(from)/slot {
			points[n-1] = point
			continue
		}
		points = append(points, point)
	}
	return points
}

// distinctFormats returns the formats without duplicates, in order
func distinctFormats(formats []Format) []Format {
	var distinct []Format
	for _, f := range formats {
		if !slices.Contains(distinct, f) {
			distinct = append(distinct, f)
		}
	}
	return distinct
}

// Ladder computes the ladders and statistics from the store
func (s *Store) Ladder(settings LadderSettings, now time.Time) (*LadderView, error) {
	matches, err := s.RatedMatches(time.Time{})
	if err != nil {
		return nil, err
	}
	ratings := ComputeRatings(settings.Rating, matches)
	results, err := s.ResultsSince(now.Add(-settings.HeadToHeadPeriod.Duration))
	if err != nil {
		return nil, err
	}
	versions, err := s.VersionTags()
	if err != nil {
		return nil, err
	}
	pool, err := s.PoolState()
	if err != nil {
		return nil, err
	}
	playing := make(map[string]bool)
	for _, state := range pool.Teams {
		playing[state.Team.Name] = state.Bot != nil && state.LeftOut == ""
	}

	view := &LadderView{
		Formats:          distinctFormats(settings.Formats),
		HeadToHead:       HeadToHeads(results),
		HeadToHeadPeriod: settings.HeadToHeadPeriod,
		Rated:            len(matches),
	}
	var from, to time.Time
	if len(matches) > 0 {
		from, to = matches[0].Time, matches[len(matches)-1].Time
	}
	for _, t := range ratings.Global.Standings() {
		team := t.Team
		view.History = append(view.History, TeamHistory{team, chartHistory(ratings.Global.History(team), from, to, chartPoints, func(commit string) string { return versions.Label(team, commit) })})
		row := LadderRow{Team: t.Team, Global: t, PerFormat: make(map[string]TeamRating), Playing: playing[t.Team]}
		for key, table := range ratings.PerFormat {
			if r := table.Rating(t.Team); r.Games > 0 {
				row.PerFormat[key] = r
			}
		}
		view.Rows = append(view.Rows, row)
		view.Teams = append(view.Teams, t.Team)
	}
	for _, f := range view.Formats {
		table := FormatTable{Format: f}
		if ratings, ok := ratings.PerFormat[f.Key()]; ok {
			table.Teams = ratings.Standings()
		}
		view.Tables = append(view.Tables, table)
	}
	return view, nil
}

// Row returns a team's row, or nil if it has not played
func (v *LadderView) Row(team string) *LadderRow {
	for i := range v.Rows {
		if v.Rows[i].Team == team {
			return &v.Rows[i]
		}
	}
	return nil
}

// TeamHistory returns a team's history alone, for its page (nil if it has not played)
func (v *LadderView) TeamHistory(team string) []TeamHistory {
	for _, h := range v.History {
		if h.Team == team {
			return []TeamHistory{h}
		}
	}
	return nil
}
