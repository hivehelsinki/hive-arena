package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testBots(n int) []Bot {
	var bots []Bot
	for i := range n {
		bots = append(bots, Bot{Name: fmt.Sprintf("bot%d", i), Image: "img", Commit: "c"})
	}
	return bots
}

func TestLineups(t *testing.T) {
	counts := map[int]int{1: 6, 2: 15, 3: 20, 4: 15, 6: 1, 7: 0}
	for n, want := range counts {
		lineups := Lineups(testBots(6), n)
		if len(lineups) != want {
			t.Errorf("C(6, %d): expected %d lineups, got %d", n, want, len(lineups))
		}
		seen := make(map[string]bool)
		for _, lineup := range lineups {
			key := fmt.Sprint(lineup)
			if seen[key] || len(lineup) != n {
				t.Errorf("duplicate or wrong size lineup %v", lineup)
			}
			seen[key] = true
		}
	}
}

func TestPlanRun(t *testing.T) {
	formats := []Format{{"a", 4}, {"b", 2}, {"c", 6}}
	matches := PlanRun(testBots(6), formats, 3, rand.New(rand.NewSource(1)))

	perRound := 15 + 15 + 1
	if len(matches) != 3*perRound {
		t.Fatalf("expected %d matches, got %d", 3*perRound, len(matches))
	}

	// Each repeat is a full round-robin: every bot plays the same number of
	// matches in each block of the plan
	for r := range 3 {
		games := make(map[string]int)
		for _, match := range matches[r*perRound : (r+1)*perRound] {
			for _, bot := range match.Bots {
				games[bot.Name]++
			}
		}
		for name, count := range games {
			if count != games["bot0"] {
				t.Errorf("repeat %d: %s plays %d matches, bot0 plays %d", r, name, count, games["bot0"])
			}
		}
	}
}

func TestPlanRunSmallPool(t *testing.T) {
	// Formats with more players than bots are skipped
	matches := PlanRun(testBots(3), []Format{{"a", 4}, {"b", 2}}, 1, rand.New(rand.NewSource(1)))
	if len(matches) != 3 {
		t.Errorf("expected the 3 pairs only, got %d matches", len(matches))
	}
}

// A match player that makes the first bot win, and fails some bots
type fakePlayer struct {
	played  int
	failing map[string]bool
	delay   time.Duration
}

func (p *fakePlayer) Run(ctx context.Context, match Match) (*MatchResult, error) {
	p.played++
	time.Sleep(p.delay)

	var flowers []uint
	var statuses []BotStatus
	for i, bot := range match.Bots {
		flowers = append(flowers, uint(len(match.Bots)-i))
		if p.failing[bot.Name] {
			statuses = append(statuses, Crashed)
		} else {
			statuses = append(statuses, Finished)
		}
	}
	return fakeResult(match.Map, match.Bots, flowers, statuses), nil
}

func newTestLadder(t *testing.T, teams []Bot, player MatchPlayer) *Ladder {
	config := &Config{}
	config.Build.Interval = Duration{time.Hour}
	config.Runner.MatchTimeout = Duration{time.Hour}
	settings := DefaultLadderSettings()
	settings.Formats = []Format{{"m", 2}}
	settings.Quota = 2
	settings.Window = 2
	settings.RandomShare = 0
	settings.MaxFailures = 2
	return &Ladder{
		Config:   config,
		Settings: NewLiveSettings("", settings),
		Store:    newTestStore(t),
		Player:   player,
		Pool:     func(ctx context.Context) ([]Bot, error) { return teams, nil },
		Rand:     rand.New(rand.NewSource(1)),
	}
}

func playTournament(ladder *Ladder, pool []Bot, deadline time.Time) (int64, error) {
	return ladder.Play(context.Background(), "tournament", TournamentRequest{Name: "test"}, pool, []Format{{"m", 2}}, 2, deadline)
}

func TestTournament(t *testing.T) {
	player := &fakePlayer{}
	ladder := newTestLadder(t, nil, player)

	runID, err := playTournament(ladder, testBots(3), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	// 3 bots: 3 pairs, twice
	if player.played != 6 {
		t.Errorf("expected 6 matches, got %d", player.played)
	}
	run, _ := ladder.Store.Run(runID)
	if run.Status != RunComplete || len(run.Bots) != 3 {
		t.Errorf("unexpected run: %+v", run)
	}
	results, _ := ladder.Store.Results([]int64{runID})
	if board := NewLeaderboard(results); len(board.Standings) != 3 || board.Standings[0].Score.Games != 4 {
		t.Errorf("unexpected standings: %+v", board.Standings)
	}
	// Not rated
	if rated, _ := ladder.Store.RatedMatches(time.Time{}); len(rated) != 0 {
		t.Errorf("tournament matches should not be rated: %d", len(rated))
	}
	if matches, _ := ladder.Store.Matches(MatchFilter{RunID: runID}); len(matches) != 6 || matches[0].Kind != TournamentMatch {
		t.Errorf("unexpected matches: %+v", matches)
	}
}

func TestTournamentDeadline(t *testing.T) {
	player := &fakePlayer{delay: 20 * time.Millisecond}
	ladder := newTestLadder(t, nil, player)

	runID, err := playTournament(ladder, testBots(3), time.Now().Add(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	run, _ := ladder.Store.Run(runID)
	if run.Status != RunIncomplete || player.played >= 6 || player.played == 0 {
		t.Errorf("expected an incomplete run, got %s after %d matches", run.Status, player.played)
	}
}

func TestLeaveOutFailing(t *testing.T) {
	store := newTestStore(t)
	bots := testBots(2)
	for range 2 {
		store.RecordMatch(MatchInfo{Kind: LadderMatch}, fakeResult("m", bots, []uint{2, 1}, []BotStatus{Finished, Crashed}))
	}

	states := []TeamState{{Team: Team{Name: "bot0"}, Bot: &bots[0]}, {Team: Team{Name: "bot1"}, Bot: &bots[1]}}
	pool, err := leaveOutFailing(store, 2, states)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) != 1 || pool[0].Name != "bot0" {
		t.Errorf("bot1 failed every match and should be left out: %+v", pool)
	}
	if states[1].LeftOut == "" || states[0].LeftOut != "" {
		t.Errorf("the state of bot1 should say why it is left out: %+v", states)
	}
}

type errorPlayer struct{ played int }

func (p *errorPlayer) Run(ctx context.Context, match Match) (*MatchResult, error) {
	p.played++
	return nil, fmt.Errorf("docker is broken")
}

func TestTournamentAbortsOnRunnerErrors(t *testing.T) {
	player := &errorPlayer{}
	ladder := newTestLadder(t, nil, player)

	runID, _ := playTournament(ladder, testBots(3), time.Now().Add(time.Hour))
	run, _ := ladder.Store.Run(runID)
	if run.Status != RunFailed || player.played != maxRunnerErrors {
		t.Errorf("expected the run to fail after %d errors, got %s after %d", maxRunnerErrors, run.Status, player.played)
	}
}

func TestEmptyTournamentsAreNotCreated(t *testing.T) {
	ladder := newTestLadder(t, nil, &fakePlayer{})
	if _, err := playTournament(ladder, testBots(1), time.Now().Add(time.Hour)); err == nil {
		t.Error("a tournament without matches should not be played")
	}
	if runs, _ := ladder.Store.Runs("", 10); len(runs) != 0 {
		t.Errorf("no run should have been recorded: %+v", runs)
	}
}

// failAfter plays n matches, then fails
type failAfter struct {
	fakePlayer
	n int
}

func (p *failAfter) Run(ctx context.Context, match Match) (*MatchResult, error) {
	if p.played >= p.n {
		return nil, fmt.Errorf("docker is broken")
	}
	return p.fakePlayer.Run(ctx, match)
}

func TestAbortedTournamentKeepsItsMatches(t *testing.T) {
	ladder := newTestLadder(t, nil, &failAfter{n: 2})
	runID, _ := playTournament(ladder, testBots(4), time.Now().Add(time.Hour))
	run, _ := ladder.Store.Run(runID)
	if run.Status != RunIncomplete {
		t.Errorf("a run aborted after playing matches should be incomplete, got %s", run.Status)
	}
	if matches, _ := ladder.Store.Matches(MatchFilter{RunID: runID}); len(matches) != 2 {
		t.Errorf("its matches should be kept: %d", len(matches))
	}
}

func TestPlanRunSamplesLargePools(t *testing.T) {
	pool := testBots(40)
	matches := PlanRun(pool, []Format{{"m", 6}}, 1, rand.New(rand.NewSource(1)))
	if len(matches) != maxLineups {
		t.Errorf("expected %d sampled lineups, got %d", maxLineups, len(matches))
	}
	seen := make(map[string]bool)
	for _, m := range matches {
		key := botNames(m.Bots)
		if seen[key] {
			t.Fatalf("duplicate lineup %s", key)
		}
		seen[key] = true
	}
	if binomial(6, 2) != 15 || binomial(40, 6) != 3838380 {
		t.Error("wrong binomial")
	}
}

func TestTopTeams(t *testing.T) {
	store := newTestStore(t)
	bots := testBots(3)
	// bot2 beats bot1 beats bot0
	for range 5 {
		store.RecordMatch(MatchInfo{Kind: LadderMatch}, fakeResult("m", bots, []uint{1, 2, 3}, []BotStatus{Finished, Finished, Finished}))
	}
	top, err := TopTeams(store, DefaultRatingParams(), 2, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].Team != "bot2" || top[1].Team != "bot1" || top[0].Commit != "c" {
		t.Errorf("unexpected top teams: %+v", top)
	}
	if _, err := TopTeams(store, DefaultRatingParams(), 4, time.Time{}); err == nil {
		t.Error("expected an error with more teams than the ladder has")
	}
	// The ladder as it was before these matches: empty
	if _, err := TopTeams(store, DefaultRatingParams(), 2, time.Now().Add(-time.Hour)); err == nil {
		t.Error("expected an error for a time before the matches")
	}
}

func TestServePlaysQueuedTournaments(t *testing.T) {
	ladder := newTestLadder(t, testBots(3), &fakePlayer{})
	ladder.Config.DataDir = t.TempDir()
	ladder.Config.MapsDir = t.TempDir()
	os.WriteFile(filepath.Join(ladder.Config.MapsDir, "m.txt"), []byte("map"), 0o644)
	var requested TournamentRequest
	ladder.TournamentBots = func(ctx context.Context, request TournamentRequest) ([]Bot, error) {
		requested = request
		return testBots(2), nil
	}
	// Even when paused
	settings := ladder.Settings.Get()
	settings.Paused = true
	ladder.Settings = NewLiveSettings("", settings)

	request := TournamentRequest{Name: "Final", Formats: []Format{{"m", 2}}, Repeats: 3, Top: 2}
	if err := QueueTournament(ladder.Config.DataDir, request); err != nil {
		t.Fatal(err)
	}
	if err := QueueTournament(ladder.Config.DataDir, request); err == nil {
		t.Error("a second request should wait for the first one")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ladder.Serve(ctx, nil)

	runs, _ := ladder.Store.Runs("tournament", 10)
	if len(runs) != 1 || runs[0].Status != RunComplete || requested.Name != "Final" || requested.Top != 2 {
		t.Fatalf("expected the tournament to be played: %+v, %+v", runs, requested)
	}
	if matches, _ := ladder.Store.Matches(MatchFilter{RunID: runs[0].ID}); len(matches) != 3 {
		t.Errorf("expected 3 matches, got %d", len(matches))
	}
	if _, err := os.Stat(tournamentRequestPath(ladder.Config.DataDir)); !os.IsNotExist(err) {
		t.Error("the request should be removed")
	}
}

func TestTournamentRequestValidation(t *testing.T) {
	config := &Config{MapsDir: t.TempDir()}
	os.WriteFile(filepath.Join(config.MapsDir, "m.txt"), []byte("map"), 0o644)
	for _, request := range []TournamentRequest{
		{Formats: nil, Repeats: 1},
		{Formats: []Format{{"m", 2}}, Repeats: 0},
		{Formats: []Format{{"nope", 2}}, Repeats: 1},
		{Formats: []Format{{"m", 2}}, Repeats: 1, Top: 1},
		{Formats: []Format{{"m", 2}}, Repeats: 1, Top: 3, Teams: []string{"a"}},
	} {
		if err := request.validate(config); err == nil {
			t.Errorf("expected an invalid request: %+v", request)
		}
	}
	if err := (&TournamentRequest{Formats: []Format{{"m", 2}}, Repeats: 1, Top: 4}).validate(config); err != nil {
		t.Error(err)
	}
}
