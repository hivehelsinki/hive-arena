package main

// A bot's placement in a match, as needed for ratings and places
type Placement struct {
	Flowers uint
	Failed  bool // crashed or did not join
}

// matchRanks returns the place of each bot in a match, in the same order:
// the first place (1-based) its group of tied bots spans, and the size of that
// group. Bots are ordered by flowers, except that failed bots are all last,
// tied with each other.
func matchRanks(placements []Placement) (ranks []int, tied []int) {
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

	ranks = make([]int, len(placements))
	tied = make([]int, len(placements))
	for i, p := range placements {
		ranks[i] = 1
		for _, other := range placements {
			if better(other, p) {
				ranks[i]++
			} else if !better(p, other) {
				tied[i]++
			}
		}
	}
	return ranks, tied
}

// One bot's place in a match (see matchRanks)
type BotResult struct {
	MatchID int64
	Team    string
	Place   int
}

type HeadToHead struct {
	Games int     `json:"games"`
	Wins  float64 `json:"wins"` // matches where the first team finished ahead, ties count half
}

// HeadToHeads returns how often each team finished ahead of each other team
// (result[a][b]), in the matches they played together. Results must be
// ordered by match.
func HeadToHeads(results []BotResult) map[string]map[string]HeadToHead {
	h2h := make(map[string]map[string]HeadToHead)
	for start := 0; start < len(results); {
		end := start
		for end < len(results) && results[end].MatchID == results[start].MatchID {
			end++
		}
		for _, a := range results[start:end] {
			for _, b := range results[start:end] {
				if a.Team == b.Team {
					continue
				}
				if h2h[a.Team] == nil {
					h2h[a.Team] = make(map[string]HeadToHead)
				}
				entry := h2h[a.Team][b.Team]
				entry.Games++
				switch {
				case a.Place < b.Place:
					entry.Wins++
				case a.Place == b.Place:
					entry.Wins += 0.5
				}
				h2h[a.Team][b.Team] = entry
			}
		}
		start = end
	}
	return h2h
}
