package main

import (
	"encoding/json"
	"sync"
	"testing"

	. "hive-arena/common"
)

func newTestSession(t *testing.T, players int) (*GameSession, []Player) {
	t.Helper()

	DevMode = true

	mapdata, err := LoadMap("../maps/tiny.txt")
	if err != nil {
		t.Fatal(err)
	}

	session := NewGameSession("test", players, "tiny", mapdata)
	var joined []Player
	for range players {
		joined = append(joined, *session.AddPlayer("team"))
	}
	return session, joined
}

// Meant to be run with -race
func TestConcurrentReadsDuringTurns(t *testing.T) {
	session, players := newTestSession(t, 2)

	var wg sync.WaitGroup
	done := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			json.Marshal(session.GetView(players[0].Token))
			json.Marshal(session.FullState())
			json.Marshal(session.Status())
			session.Started()
			session.IsOver()
		}
	}()

	for turn := range uint(20) {
		for _, player := range players {
			if err := session.SetOrders(player.ID, turn, []*Order{}); err != nil {
				t.Fatal(err)
			}
		}
	}

	close(done)
	wg.Wait()

	if session.FullState().Turn != 20 {
		t.Errorf("expected 20 turns to be played, got %d", session.FullState().Turn)
	}
}

func TestOrdersForAnotherTurnAreRejected(t *testing.T) {
	session, players := newTestSession(t, 2)

	if err := session.SetOrders(players[0].ID, 1, []*Order{}); err == nil {
		t.Error("orders for a future turn should be rejected")
	}

	for _, player := range players {
		if err := session.SetOrders(player.ID, 0, []*Order{}); err != nil {
			t.Fatal(err)
		}
	}

	if err := session.SetOrders(players[0].ID, 0, []*Order{}); err == nil {
		t.Error("orders for a past turn should be rejected")
	}
	if err := session.SetOrders(players[0].ID, 1, []*Order{}); err != nil {
		t.Errorf("orders for the current turn should be accepted: %s", err)
	}
}

func TestOrdersBeforeStartAreRejected(t *testing.T) {
	DevMode = true

	mapdata, err := LoadMap("../maps/tiny.txt")
	if err != nil {
		t.Fatal(err)
	}

	session := NewGameSession("test", 2, "tiny", mapdata)
	player := session.AddPlayer("team")

	if err := session.SetOrders(player.ID, 0, []*Order{}); err == nil {
		t.Error("orders before the game starts should be rejected")
	}
}

func TestEmptyOrdersCountAsPlayed(t *testing.T) {
	session, players := newTestSession(t, 2)

	// A JSON payload of null decodes to a nil slice
	for _, player := range players {
		if err := session.SetOrders(player.ID, 0, nil); err != nil {
			t.Fatal(err)
		}
	}

	if turn := session.FullState().Turn; turn != 1 {
		t.Errorf("turn should be processed once all players have posted, current turn is %d", turn)
	}
}
