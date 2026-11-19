package main

import "testing"

func TestHeadToHeads(t *testing.T) {
	results := []BotResult{
		// Match 1: a first, b and c tied second
		{1, "a", 1}, {1, "b", 2}, {1, "c", 2},
		// Match 2: b beats a
		{2, "a", 2}, {2, "b", 1},
	}
	h2h := HeadToHeads(results)
	if h := h2h["a"]["b"]; h.Games != 2 || h.Wins != 1 {
		t.Errorf("a vs b: expected 1 win in 2 matches, got %+v", h)
	}
	if h := h2h["b"]["c"]; h.Games != 1 || h.Wins != 0.5 {
		t.Errorf("b vs c: a tie counts half, got %+v", h)
	}
	if h := h2h["c"]["a"]; h.Games != 1 || h.Wins != 0 {
		t.Errorf("c vs a: expected a loss, got %+v", h)
	}
	if _, ok := h2h["a"]["a"]; ok {
		t.Error("a team does not play itself")
	}
}

func TestMatchPlaces(t *testing.T) {
	match := MatchSummary{Bots: []MatchBot{
		{Team: "a", Flowers: 3, Status: Finished},
		{Team: "b", Flowers: 9, Status: Crashed},
		{Team: "c", Flowers: 5, Status: Finished},
		{Team: "d", Flowers: 3, Status: Finished},
	}}
	match.setPlaces()
	places := map[string]int{}
	for _, b := range match.Bots {
		places[b.Team] = b.Place
	}
	// c first; a and d tied second; b crashed, last despite its flowers
	if places["c"] != 1 || places["a"] != 2 || places["d"] != 2 || places["b"] != 4 {
		t.Errorf("unexpected places: %v", places)
	}
	if byPlace := match.ByPlace(); byPlace[0].Team != "c" || byPlace[3].Team != "b" {
		t.Errorf("unexpected order: %+v", byPlace)
	}

	void := MatchSummary{Void: true, Bots: []MatchBot{{Team: "a", Flowers: 3, Status: Finished}}}
	void.setPlaces()
	if void.Bots[0].Place != 0 {
		t.Error("bots of a void match have no place")
	}
}
