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

	for range 20 {
		for _, player := range players {
			session.SetOrders(player.ID, []*Order{})
		}
	}

	close(done)
	wg.Wait()

	if session.FullState().Turn != 20 {
		t.Errorf("expected 20 turns to be played, got %d", session.FullState().Turn)
	}
}
