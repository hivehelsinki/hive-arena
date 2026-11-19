package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// rankedRatings returns ratings where bots with a higher index are better,
// in a format
func rankedRatings(settings LadderSettings, pool []Bot, format Format) *RatingTables {
	var matches []RatedMatch
	for round := range 20 {
		for i := range pool {
			for j := i + 1; j < len(pool); j++ {
				matches = append(matches, RatedMatch{
					ID: int64(len(matches)), Time: time.Unix(int64(round), 0), Format: format.Key(),
					Bots: []RatedBot{{Team: pool[i].Name, Placement: Placement{Flowers: 0}}, {Team: pool[j].Name, Placement: Placement{Flowers: 1}}},
				})
			}
		}
	}
	return ComputeRatings(settings.Rating, matches)
}

// onlyBelowQuota returns counts where every team has met its quota, except one
func onlyBelowQuota(pool []Bot, formats []Format, quota int, team string) map[string]map[string]int {
	counts := make(map[string]map[string]int)
	for _, bot := range pool {
		counts[bot.Name] = make(map[string]int)
		for _, f := range formats {
			if bot.Name != team {
				counts[bot.Name][f.Key()] = quota
			}
		}
	}
	return counts
}

func testSettings(formats ...Format) LadderSettings {
	settings := DefaultLadderSettings()
	settings.Formats = formats
	settings.Window = 2
	settings.RandomShare = 0
	return settings
}

func opponents(match *Match, challenger string) []string {
	var names []string
	for _, bot := range match.Bots {
		if bot.Name != challenger {
			names = append(names, bot.Name)
		}
	}
	slices.Sort(names)
	return names
}

func TestPickMatchChallengesTeamsAbove(t *testing.T) {
	pool := testBots(10)
	format := Format{"m", 2}
	settings := testSettings(format)
	ratings := rankedRatings(settings, pool, format)

	cases := []struct {
		challenger string
		allowed    []string
	}{
		{"bot3", []string{"bot4", "bot5"}},
		{"bot8", []string{"bot9", "bot7"}}, // one above, then below
		{"bot9", []string{"bot8", "bot7"}}, // the leader challenges the teams below
	}
	for _, c := range cases {
		counts := onlyBelowQuota(pool, settings.Formats, settings.Quota, c.challenger)
		seen := make(map[string]bool)
		for seed := range 40 {
			match := pickMatch(pool, settings, counts, ratings, rand.New(rand.NewSource(int64(seed))))
			if match == nil || len(match.Bots) != 2 || match.Map != "m" {
				t.Fatalf("unexpected match: %+v", match)
			}
			opponent := opponents(match, c.challenger)
			if len(opponent) != 1 || !slices.Contains(c.allowed, opponent[0]) {
				t.Fatalf("%s should challenge one of %v, got %v", c.challenger, c.allowed, opponent)
			}
			seen[opponent[0]] = true
		}
		if len(seen) != len(c.allowed) {
			t.Errorf("%s: expected every allowed opponent to be drawn, got %v", c.challenger, seen)
		}
	}
}

func TestPickMatchWindowFitsTheFormat(t *testing.T) {
	pool := testBots(10)
	format := Format{"m", 6}
	settings := testSettings(format)
	ratings := rankedRatings(settings, pool, format)

	// 5 opponents are needed: the 5 teams above
	counts := onlyBelowQuota(pool, settings.Formats, settings.Quota, "bot3")
	match := pickMatch(pool, settings, counts, ratings, rand.New(rand.NewSource(1)))
	if got := opponents(match, "bot3"); !slices.Equal(got, []string{"bot4", "bot5", "bot6", "bot7", "bot8"}) {
		t.Errorf("unexpected opponents: %v", got)
	}
}

func TestPickMatchRandomOpponents(t *testing.T) {
	pool := testBots(10)
	format := Format{"m", 2}
	settings := testSettings(format)
	settings.RandomShare = 1
	ratings := rankedRatings(settings, pool, format)
	counts := onlyBelowQuota(pool, settings.Formats, settings.Quota, "bot9")

	seen := make(map[string]bool)
	for seed := range 100 {
		match := pickMatch(pool, settings, counts, ratings, rand.New(rand.NewSource(int64(seed))))
		seen[opponents(match, "bot9")[0]] = true
	}
	if !seen["bot0"] || len(seen) < 7 {
		t.Errorf("random matches should reach the whole field: %v", seen)
	}
}

func TestPickMatchQuotas(t *testing.T) {
	pool := testBots(3)
	settings := testSettings(Format{"a", 2}, Format{"b", 3}, Format{"c", 4})
	ratings := ComputeRatings(settings.Rating, nil)
	rng := rand.New(rand.NewSource(1))

	// The team and format furthest below the quota
	counts := map[string]map[string]int{
		"bot0": {"a:2": 20, "b:3": 20},
		"bot1": {"a:2": 20, "b:3": 15},
		"bot2": {"a:2": 18, "b:3": 20},
	}
	match := pickMatch(pool, settings, counts, ratings, rng)
	if match == nil || match.Map != "b" || len(match.Bots) != 3 {
		t.Errorf("expected bot1 to challenge in b: %+v", match)
	}

	// Quotas met: nothing to play (c needs more teams than there are)
	counts["bot1"]["b:3"], counts["bot2"]["a:2"] = 20, 20
	if match := pickMatch(pool, settings, counts, ratings, rng); match != nil {
		t.Errorf("every quota is met, expected no match: %+v", match)
	}
}

func TestServePlaysUntilQuotasAreMet(t *testing.T) {
	player := &fakePlayer{}
	ladder := newTestLadder(t, testBots(3), player)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := ladder.Serve(ctx, nil); err != nil {
		t.Fatal(err)
	}

	// Quota 2 with 3 teams in pairs: at least 3 matches, and only a few more
	counts, _ := ladder.Store.MatchCounts(time.Now().Add(-time.Hour))
	for _, bot := range testBots(3) {
		if counts[bot.Name]["m:2"] < 2 {
			t.Errorf("%s played %d matches, below its quota", bot.Name, counts[bot.Name]["m:2"])
		}
	}
	if player.played < 3 || player.played > 4 {
		t.Errorf("expected 3 or 4 matches, got %d", player.played)
	}
	if rated, _ := ladder.Store.RatedMatches(time.Time{}); len(rated) != player.played {
		t.Errorf("expected every match to be recorded and rated, got %d", len(rated))
	}
	if !strings.HasPrefix(Activity(), "") || Heartbeat().IsZero() {
		t.Error("expected a heartbeat")
	}
}

func TestServePaused(t *testing.T) {
	player := &fakePlayer{}
	ladder := newTestLadder(t, testBots(3), player)
	settings := ladder.Settings.Get()
	settings.Paused = true
	ladder.Settings = NewLiveSettings("", settings)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ladder.Serve(ctx, nil)
	if player.played != 0 {
		t.Errorf("a paused ladder should not play, got %d matches", player.played)
	}
}

func TestServeWaitsAfterRunnerErrors(t *testing.T) {
	player := &errorPlayer{}
	ladder := newTestLadder(t, testBots(3), player)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ladder.Serve(ctx, nil)
	if player.played != maxRunnerErrors {
		t.Errorf("expected the matchmaker to wait after %d errors, got %d matches", maxRunnerErrors, player.played)
	}
	if !strings.Contains(LastError(), "failed in a row") {
		t.Errorf("expected the error to be reported: %q", LastError())
	}
	setLastError("")
}

func TestServeRebuildsThePoolWhenWoken(t *testing.T) {
	ladder := newTestLadder(t, testBots(2), &fakePlayer{})
	resolutions := 0
	ladder.Pool = func(ctx context.Context) ([]Bot, error) {
		resolutions++
		return testBots(2), nil
	}
	wake := make(chan struct{}, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() {
		time.Sleep(100 * time.Millisecond)
		wake <- struct{}{}
	}()
	ladder.Serve(ctx, wake)
	if resolutions != 2 {
		t.Errorf("expected the pool to be built at the start and when woken, got %d", resolutions)
	}
}

func TestLiveSettingsReload(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "maps"), 0o755)
	os.WriteFile(filepath.Join(dir, "maps", "m.txt"), []byte("map"), 0o644)
	path := filepath.Join(dir, "ladder.json")
	write := func(quota string) {
		config := fmt.Sprintf(`{"mapsDir": %q, "registry": {"path": "teams.json"}, "build": {"smokeMap": "m"},
			"ladder": {"formats": [{"map": "m", "players": 2}], "quota": %s}}`, filepath.Join(dir, "maps"), quota)
		os.WriteFile(path, []byte(config), 0o644)
	}
	touch := func(i int) {
		at := time.Now().Add(time.Duration(i) * time.Minute)
		os.Chtimes(path, at, at)
	}

	write("20")
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	live := NewLiveSettings(path, config.Ladder)
	if live.Reload() {
		t.Error("an unchanged file should not reload")
	}

	write("50")
	touch(1)
	if !live.Reload() || live.Get().Quota != 50 {
		t.Errorf("expected the new quota, got %d", live.Get().Quota)
	}

	write("0")
	touch(2)
	if live.Reload() || live.Get().Quota != 50 || !strings.Contains(live.Warning(), "quota") {
		t.Errorf("an invalid file should keep the previous settings, with a warning: %d, %q", live.Get().Quota, live.Warning())
	}

	write("30")
	touch(3)
	if !live.Reload() || live.Get().Quota != 30 || live.Warning() != "" {
		t.Errorf("a fixed file should apply and clear the warning: %d, %q", live.Get().Quota, live.Warning())
	}
}

func TestRandomMapFormats(t *testing.T) {
	config := &Config{MapsDir: t.TempDir()}
	settings := DefaultLadderSettings()
	settings.Formats = []Format{{"random", 2}, {"random", 6}}
	if err := settings.validate(config); err != nil {
		t.Errorf("random maps should be valid formats: %v", err)
	}
	settings.Formats = []Format{{"random", 5}}
	if err := settings.validate(config); err == nil {
		t.Error("there are no random maps for 5 players")
	}
}
