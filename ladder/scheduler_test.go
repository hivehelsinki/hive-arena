package main

import (
	"context"
	"fmt"
	"math/rand"
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

func TestNextRunTime(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Helsinki")
	schedule := Schedule{Times: []string{"16:00", "08:00"}}

	cases := []struct{ now, want string }{
		{"2026-11-17 07:00", "2026-11-17 08:00"},
		{"2026-11-17 08:00", "2026-11-17 16:00"},
		{"2026-11-17 12:00", "2026-11-17 16:00"},
		{"2026-11-17 23:00", "2026-11-18 08:00"},
	}
	for _, c := range cases {
		now, _ := time.ParseInLocation("2006-01-02 15:04", c.now, loc)
		want, _ := time.ParseInLocation("2006-01-02 15:04", c.want, loc)
		if got := schedule.NextRunTime(now, loc); !got.Equal(want) {
			t.Errorf("after %s: expected %s, got %s", c.now, want, got)
		}
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
	config := &Config{
		Schedule: Schedule{
			Formats:     []Format{{"m", 2}},
			Repeats:     2,
			Window:      2,
			MaxFailures: 2,
		},
	}
	return &Ladder{
		Config: config,
		Store:  newTestStore(t),
		Player: player,
		Pool:   func(ctx context.Context) ([]Bot, error) { return teams, nil },
		Rand:   rand.New(rand.NewSource(1)),
	}
}

func TestRunOnce(t *testing.T) {
	player := &fakePlayer{}
	ladder := newTestLadder(t, testBots(3), player)

	runID, err := ladder.RunOnce(context.Background(), time.Now().Add(time.Hour))
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
	board, _, _ := ladder.Store.Leaderboard(2)
	if len(board.Standings) != 3 || board.Standings[0].Score.Games != 4 {
		t.Errorf("unexpected leaderboard: %+v", board.Standings)
	}
}

func TestRunOnceDeadline(t *testing.T) {
	player := &fakePlayer{delay: 20 * time.Millisecond}
	ladder := newTestLadder(t, testBots(3), player)

	runID, err := ladder.RunOnce(context.Background(), time.Now().Add(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	run, _ := ladder.Store.Run(runID)
	if run.Status != RunIncomplete || player.played >= 6 || player.played == 0 {
		t.Errorf("expected an incomplete run, got %s after %d matches", run.Status, player.played)
	}
}

func TestLeaveOutFailing(t *testing.T) {
	player := &fakePlayer{failing: map[string]bool{"bot1": true}}
	bots := testBots(3)
	ladder := newTestLadder(t, bots, player)
	ladder.RunOnce(context.Background(), time.Now().Add(time.Hour))

	states := []TeamState{{Team: Team{Name: "bot0"}, Bot: &bots[0]}, {Team: Team{Name: "bot1"}, Bot: &bots[1]}}
	pool, err := leaveOutFailing(ladder.Store, 2, states)
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

func TestRunOnceAbortsOnRunnerErrors(t *testing.T) {
	player := &errorPlayer{}
	ladder := newTestLadder(t, testBots(3), player)

	runID, _ := ladder.RunOnce(context.Background(), time.Now().Add(time.Hour))
	run, _ := ladder.Store.Run(runID)
	if run.Status != RunFailed || player.played != maxRunnerErrors {
		t.Errorf("expected the run to fail after %d errors, got %s after %d", maxRunnerErrors, run.Status, player.played)
	}
}

func TestEmptyRunsAreNotCreated(t *testing.T) {
	ladder := newTestLadder(t, testBots(1), &fakePlayer{})
	if _, err := ladder.RunOnce(context.Background(), time.Now().Add(time.Hour)); err == nil {
		t.Error("a run without matches should not be played")
	}
	if _, err := ladder.RunOnce(context.Background(), time.Now().Add(-time.Minute)); err == nil {
		t.Error("a run past its deadline should not be played")
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

func TestAbortedRunKeepsItsMatches(t *testing.T) {
	ladder := newTestLadder(t, testBots(4), &failAfter{n: 2})
	runID, _ := ladder.RunOnce(context.Background(), time.Now().Add(time.Hour))
	run, _ := ladder.Store.Run(runID)
	if run.Status != RunIncomplete {
		t.Errorf("a run aborted after playing matches should be incomplete, got %s", run.Status)
	}
	if board, runs, _ := ladder.Store.Leaderboard(2); len(runs) != 1 || len(board.Standings) == 0 {
		t.Errorf("its matches should count: %+v", board)
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

func TestLastDue(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Helsinki")
	schedule := Schedule{Times: []string{"08:00", "16:00"}}
	at := func(day, hour int) time.Time { return time.Date(2026, 3, day, hour, 0, 0, 0, loc) }

	// A run due at 8:00 ended at 17:00: the 16:00 run is due
	if got := schedule.LastDue(at(2, 8), at(2, 17), loc); !got.Equal(at(2, 16)) {
		t.Errorf("expected 16:00, got %s", got)
	}
	// Ended at 9:00: the 8:00 run itself
	if got := schedule.LastDue(at(2, 8), at(2, 9), loc); !got.Equal(at(2, 8)) {
		t.Errorf("expected 8:00, got %s", got)
	}
	// Several missed: the last one only
	if got := schedule.LastDue(at(2, 8), at(4, 7), loc); !got.Equal(at(3, 16)) {
		t.Errorf("expected the day before at 16:00, got %s", got)
	}
}

func TestServeBuildsBetweenRuns(t *testing.T) {
	ladder := newTestLadder(t, testBots(2), &fakePlayer{})
	ladder.Config.Schedule.Times = []string{time.Now().UTC().Add(12 * time.Hour).Format("15:04")}
	ladder.Config.Build.Interval = Duration{20 * time.Millisecond}
	resolutions := 0
	ladder.Pool = func(ctx context.Context) ([]Bot, error) {
		resolutions++
		return testBots(2), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := ladder.Serve(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if resolutions < 3 {
		t.Errorf("expected new versions to be built regularly, got %d resolutions", resolutions)
	}
	if runs, _ := ladder.Store.Runs("", 10); len(runs) != 0 {
		t.Errorf("building between runs should not play: %+v", runs)
	}
}
