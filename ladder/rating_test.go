package main

import (
	"math"
	"testing"
	"time"
)

// Reference values from the openskill Python library (6.2.0), PlackettLuce
// with its defaults (the same as DefaultRatingParams, without VersionSigma)
func TestRateMatchesOpenSkill(t *testing.T) {
	cases := []struct {
		name   string
		before []Rating
		ranks  []int
		after  []Rating
	}{
		{"two players",
			[]Rating{{25, 25.0 / 3}, {25, 25.0 / 3}}, []int{1, 2},
			[]Rating{{27.6353894931, 8.0659014135}, {22.3646105069, 8.0659014135}}},
		{"four players",
			[]Rating{{30, 4}, {25, 25.0 / 3}, {20, 6}, {27, 2}}, []int{2, 1, 4, 3},
			[]Rating{{30.264079202, 3.97876041}, {28.8132880236, 8.1736175527}, {18.0573109689, 5.8707706337}, {26.9300769361, 1.999788353}}},
		{"ties",
			[]Rating{{25, 25.0 / 3}, {28, 5}, {22, 7}}, []int{1, 1, 3},
			[]Rating{{25.852864795, 8.1369434528}, {28.1666150202, 4.973024716}, {21.0716724734, 6.9144557755}}},
		{"six players",
			[]Rating{{25, 8}, {26, 7}, {24, 6}, {30, 3}, {18, 5}, {25, 1}}, []int{3, 1, 6, 2, 5, 4},
			[]Rating{{26.3347929879, 7.7880918309}, {28.3770916274, 6.9647254719}, {20.6946954689, 5.8729627324}, {30.268033003, 2.998077961}, {17.8609042174, 4.9495756604}, {24.998228818, 1.0033898501}}},
		{"failed tied last",
			[]Rating{{25, 25.0 / 3}, {25, 25.0 / 3}, {25, 25.0 / 3}, {25, 25.0 / 3}}, []int{1, 2, 3, 3},
			[]Rating{{27.7952526725, 8.2635717913}, {26.5529181514, 8.1796179884}, {22.8259145881, 8.0841278802}, {22.8259145881, 8.0841278802}}},
	}

	params := DefaultRatingParams()
	for _, c := range cases {
		got := params.rate(c.before, c.ranks)
		for i := range got {
			if math.Abs(got[i].Mu-c.after[i].Mu) > 1e-8 || math.Abs(got[i].Sigma-c.after[i].Sigma) > 1e-8 {
				t.Errorf("%s, player %d: got %+v, expected %+v", c.name, i, got[i], c.after[i])
			}
		}
	}
}

func TestMatchRanks(t *testing.T) {
	ranks, tied := matchRanks([]Placement{{Flowers: 3}, {Flowers: 5}, {Failed: true, Flowers: 9}, {Flowers: 3}, {Failed: true}})
	expected, expectedTied := []int{2, 1, 4, 2, 4}, []int{2, 1, 2, 2, 2}
	for i := range ranks {
		if ranks[i] != expected[i] || tied[i] != expectedTied[i] {
			t.Fatalf("got ranks %v (tied %v), expected %v (%v)", ranks, tied, expected, expectedTied)
		}
	}
}

func match(id int64, format string, bots ...RatedBot) RatedMatch {
	return RatedMatch{ID: id, Time: time.Unix(id, 0), Format: format, Bots: bots}
}

func bot(team, commit string, flowers uint) RatedBot {
	return RatedBot{Team: team, Commit: commit, Placement: Placement{Flowers: flowers}}
}

func TestNewTeamsStartAtTheBottom(t *testing.T) {
	ratings := ComputeRatings(DefaultRatingParams(), nil)
	r := ratings.Global.Rating("new")
	if r.Games != 0 || math.Abs(r.Rating.Ordinal()) > 1e-9 {
		t.Errorf("a new team should start at ordinal 0: %+v", r)
	}
}

func TestRatingsFollowResults(t *testing.T) {
	params := DefaultRatingParams()
	var matches []RatedMatch
	// a always beats b, which always beats c, in two formats
	for i := range 30 {
		format := "a:3"
		if i%2 == 0 {
			format = "b:3"
		}
		matches = append(matches, match(int64(i), format, bot("c", "c1", 1), bot("a", "a1", 9), bot("b", "b1", 5)))
	}
	ratings := ComputeRatings(params, matches)

	for _, table := range []*RatingTable{ratings.Global, ratings.PerFormat["a:3"], ratings.PerFormat["b:3"]} {
		standings := table.Standings()
		if len(standings) != 3 || standings[0].Team != "a" || standings[1].Team != "b" || standings[2].Team != "c" {
			t.Fatalf("unexpected standings: %+v", standings)
		}
	}
	if g, f := ratings.Global.Rating("a"), ratings.PerFormat["a:3"].Rating("a"); g.Games != 30 || f.Games != 15 {
		t.Errorf("expected 30 matches globally and 15 in a format, got %d and %d", g.Games, f.Games)
	}
	// Uncertainty shrinks with matches (slowly with Plackett-Luce)
	if s := ratings.Global.Rating("a").Rating.Sigma; s >= DefaultRatingParams().Sigma || s < 1 {
		t.Errorf("unexpected uncertainty after 30 matches: %f", s)
	}
}

func TestFailedBotsAreLast(t *testing.T) {
	failed := bot("crashy", "x", 100)
	failed.Failed = true
	ratings := ComputeRatings(DefaultRatingParams(), []RatedMatch{match(1, "m:2", failed, bot("slow", "y", 0))})
	if standings := ratings.Global.Standings(); standings[0].Team != "slow" {
		t.Errorf("a failed bot should lose whatever its flowers: %+v", standings)
	}
}

func TestNewVersionsRaiseUncertainty(t *testing.T) {
	params := DefaultRatingParams()
	var matches []RatedMatch
	// Evenly matched, so that every match is informative
	for i := range 400 {
		matches = append(matches, match(int64(i), "m:2", bot("a", "v1", uint(i%2)), bot("b", "b1", uint(1-i%2))))
	}
	before := ComputeRatings(params, matches).Global.Rating("a")
	if before.Rating.Sigma >= params.VersionSigma {
		t.Fatalf("expected an uncertainty under %f after 400 even matches, got %f", params.VersionSigma, before.Rating.Sigma)
	}

	// A new version that wins: with the uncertainty raised, it moves faster
	next := match(1000, "m:2", bot("a", "v2", 3), bot("b", "b1", 2))
	bumped := ComputeRatings(params, append(matches, next)).Global.Rating("a")
	params.VersionSigma = 0
	plain := ComputeRatings(params, append(matches, next)).Global.Rating("a")

	if bumped.Rating.Mu-before.Rating.Mu <= 2*(plain.Rating.Mu-before.Rating.Mu) {
		t.Errorf("a new version should move faster: %f -> %f with the bump, %f without", before.Rating.Mu, bumped.Rating.Mu, plain.Rating.Mu)
	}
	if bumped.Commit != "v2" || !bumped.Since.Equal(next.Time) || before.Since.Unix() != 0 {
		t.Errorf("unexpected versions: %+v, %+v", before, bumped)
	}
}

func TestChartHistory(t *testing.T) {
	params := DefaultRatingParams()
	var matches []RatedMatch
	for i := range 1000 {
		commit := "v1aaaaaaaa"
		if i >= 600 {
			commit = "v2bbbbbbbb"
		}
		matches = append(matches, match(int64(i), "m:2", bot("a", commit, uint(i%2)), bot("b", "b1", uint(1-i%2))))
	}
	ratings := ComputeRatings(params, matches)
	history := ratings.Global.History("a")
	if len(history) != 1000 || history[999].Rating != ratings.Global.Rating("a").Rating {
		t.Fatalf("expected the rating after every match: %d points", len(history))
	}

	points := chartHistory(history, time.Unix(0, 0), time.Unix(999, 0), 100, func(commit string) string { return commit[:8] })
	if len(points) > 105 || len(points) < 90 {
		t.Errorf("expected about 100 points, got %d", len(points))
	}
	var versions []string
	for _, p := range points {
		if p.Version != "" {
			versions = append(versions, p.Version)
		}
	}
	if len(versions) != 2 || versions[0] != "v1aaaaaa" || versions[1] != "v2bbbbbb" {
		t.Errorf("expected the start of both versions: %v", versions)
	}
	if last := points[len(points)-1]; last.Time != 999 {
		t.Errorf("the last point should be the latest rating: %+v", last)
	}
}
