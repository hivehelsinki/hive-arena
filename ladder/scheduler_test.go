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
		HouseBots: []Bot{{Name: "house", Image: "house", Commit: "h"}},
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

	// 4 bots with the house bot: 6 pairs, twice
	if player.played != 12 {
		t.Errorf("expected 12 matches, got %d", player.played)
	}
	run, _ := ladder.Store.Run(runID)
	if run.Status != RunComplete || len(run.Bots) != 4 {
		t.Errorf("unexpected run: %+v", run)
	}
	board, _, _ := ladder.Store.Leaderboard(2)
	if len(board.Standings) != 4 || board.Standings[0].Score.Games != 6 {
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
	if run.Status != RunIncomplete || player.played >= 12 || player.played == 0 {
		t.Errorf("expected an incomplete run, got %s after %d matches", run.Status, player.played)
	}
}

func TestRunOnceLeavesFailingBotsOut(t *testing.T) {
	player := &fakePlayer{failing: map[string]bool{"bot1": true}}
	ladder := newTestLadder(t, testBots(2), player)

	ladder.RunOnce(context.Background(), time.Now().Add(time.Hour))
	runID, _ := ladder.RunOnce(context.Background(), time.Now().Add(time.Hour))

	run, _ := ladder.Store.Run(runID)
	for _, bot := range run.Bots {
		if bot.Name == "bot1" {
			t.Errorf("bot1 failed every match and should be left out: %+v", run.Bots)
		}
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
