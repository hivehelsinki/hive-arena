package main

import (
	"slices"
	"time"
)

// A team in the ladder, as shown on the website
type LadderRow struct {
	Team   string     `json:"team"`
	Global TeamRating `json:"global"`
	// Ratings in each format the team played (see Format.Key)
	PerFormat map[string]TeamRating `json:"perFormat"`
	// Mean points and matches over the statistics period
	Stats *Standing `json:"stats,omitempty"`
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
	// Mean points and head-to-head results over the statistics period
	Stats       Leaderboard `json:"stats"`
	StatsPeriod Duration    `json:"statsPeriod"`
	Teams       []string    `json:"-"` // in ladder order, for the head-to-head table
	Rated       int         `json:"ratedMatches"`
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
	results, err := s.ResultsSince(now.Add(-settings.StatsPeriod.Duration))
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
		Formats:     distinctFormats(settings.Formats),
		Stats:       NewLeaderboard(results),
		StatsPeriod: settings.StatsPeriod,
		Rated:       len(matches),
	}
	stats := make(map[string]*Standing)
	for i := range view.Stats.Standings {
		stats[view.Stats.Standings[i].Team] = &view.Stats.Standings[i]
	}
	for _, t := range ratings.Global.Standings() {
		row := LadderRow{Team: t.Team, Global: t, PerFormat: make(map[string]TeamRating), Stats: stats[t.Team], Playing: playing[t.Team]}
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
