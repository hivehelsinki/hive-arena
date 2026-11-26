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

func TestLeaveOutFailing(t *testing.T) {
	store := newTestStore(t)
	bots := testBots(2)
	for range 2 {
		store.RecordMatch(MatchInfo{}, fakeResult("m", bots, []uint{2, 1}, []BotStatus{Finished, Crashed}))
	}

	states := []TeamState{{Team: Team{Name: "bot0", ID: "bot0"}, Bot: &bots[0]}, {Team: Team{Name: "bot1", ID: "bot1"}, Bot: &bots[1]}}
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
