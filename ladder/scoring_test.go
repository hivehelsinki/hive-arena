package main

import (
	"math"
	"slices"
	"testing"
)

func TestMatchPointsTable(t *testing.T) {
	expected := map[int][]int{
		2: {60, 0},
		3: {60, 30, 0},
		4: {60, 40, 20, 0},
		6: {60, 48, 36, 24, 12, 0},
	}
	for n, want := range expected {
		var placements []Placement
		for i := range n {
			placements = append(placements, Placement{Flowers: uint(100 - i)})
		}
		if got := MatchPoints(placements); !slices.Equal(got, want) {
			t.Errorf("%d players: expected %v, got %v", n, want, got)
		}
	}
}

func TestMatchPointsTies(t *testing.T) {
	cases := []struct {
		placements []Placement
		want       []int
	}{
		// Two tied for first in a 4-player game
		{[]Placement{{Flowers: 5}, {Flowers: 5}, {Flowers: 3}, {Flowers: 0}}, []int{50, 50, 20, 0}},
		// All tied
		{[]Placement{{Flowers: 0}, {Flowers: 0}, {Flowers: 0}}, []int{30, 30, 30}},
		// Two tied for second in a 3-player game
		{[]Placement{{Flowers: 1}, {Flowers: 1}, {Flowers: 4}}, []int{15, 15, 60}},
		// Order of the input does not matter
		{[]Placement{{Flowers: 0}, {Flowers: 9}, {Flowers: 4}, {Flowers: 4}, {Flowers: 1}, {Flowers: 2}}, []int{0, 60, 42, 42, 12, 24}},
	}
	for _, c := range cases {
		if got := MatchPoints(c.placements); !slices.Equal(got, c.want) {
			t.Errorf("%+v: expected %v, got %v", c.placements, c.want, got)
		}
	}
}

func TestMatchPointsFailedBotsAreLast(t *testing.T) {
	// A failed bot ranks last whatever its flowers, tied with other failed bots
	got := MatchPoints([]Placement{
		{Flowers: 10, Failed: true},
		{Flowers: 1},
		{Flowers: 0},
		{Flowers: 0, Failed: true},
	})
	if want := []int{10, 60, 40, 10}; !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestMatchPointsAreIntegersForAllTies(t *testing.T) {
	// Every way of tying consecutive places gives exact integers
	for _, n := range []int{2, 3, 4, 6} {
		for first := 0; first < n; first++ {
			for last := first; last < n; last++ {
				sum := 0.0
				for place := first + 1; place <= last+1; place++ {
					sum += MaxPoints * float64(n-place) / float64(n-1)
				}
				avg := sum / float64(last-first+1)
				if avg != math.Trunc(avg) {
					t.Errorf("%d players, places %d to %d tied: %v is not an integer", n, first+1, last+1, avg)
				}
			}
		}
	}
}

func TestNewScore(t *testing.T) {
	if s := NewScore(nil); s.Games != 0 || s.Mean != 0 {
		t.Errorf("unexpected empty score: %+v", s)
	}
	s := NewScore([]int{60, 0, 60, 0})
	if s.Games != 4 || s.Mean != 30 || math.Abs(s.StdErr-math.Sqrt(1200.0/4)) > 1e-9 {
		t.Errorf("unexpected score: %+v", s)
	}
}

func TestLeaderboard(t *testing.T) {
	results := []BotResult{
		{MatchID: 1, Map: "m1", Team: "a", Points: 60, Commit: "a1"},
		{MatchID: 1, Map: "m1", Team: "b", Points: 30},
		{MatchID: 1, Map: "m1", Team: "c", Points: 0},
		{MatchID: 2, Map: "m2", Team: "a", Points: 0, Commit: "a2"},
		{MatchID: 2, Map: "m2", Team: "b", Points: 60},
		{MatchID: 3, Map: "m2", Team: "b", Points: 30},
		{MatchID: 3, Map: "m2", Team: "c", Points: 30},
	}
	board := NewLeaderboard(results)

	var order []string
	for _, s := range board.Standings {
		order = append(order, s.Team)
	}
	if !slices.Equal(order, []string{"b", "a", "c"}) {
		t.Errorf("unexpected order %v", order)
	}
	if a := board.Standings[1]; a.Commit != "a2" || a.Score.Games != 2 || a.PerMap["m1"].Mean != 60 {
		t.Errorf("unexpected standing: %+v", a)
	}

	if h := board.HeadToHead["a"]["b"]; h.Games != 2 || h.Wins != 1 {
		t.Errorf("a vs b: %+v", h)
	}
	if h := board.HeadToHead["b"]["c"]; h.Games != 2 || h.Wins != 1.5 {
		t.Errorf("b vs c: %+v", h)
	}
	if h := board.HeadToHead["c"]["b"]; h.Games != 2 || h.Wins != 0.5 {
		t.Errorf("c vs b: %+v", h)
	}
}
