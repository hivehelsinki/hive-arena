package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hive-arena/common"
)

func newTestSession(t *testing.T, players int) (*GameSession, []Player) {
	t.Helper()

	DevMode = true
	HistoryDir = t.TempDir()

	mapdata, err := common.LoadMap("../maps/tiny.txt")
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
			if err := session.SetOrders(player.ID, turn, []*common.Order{}); err != nil {
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

	if err := session.SetOrders(players[0].ID, 1, []*common.Order{}); err == nil {
		t.Error("orders for a future turn should be rejected")
	}

	for _, player := range players {
		if err := session.SetOrders(player.ID, 0, []*common.Order{}); err != nil {
			t.Fatal(err)
		}
	}

	if err := session.SetOrders(players[0].ID, 0, []*common.Order{}); err == nil {
		t.Error("orders for a past turn should be rejected")
	}
	if err := session.SetOrders(players[0].ID, 1, []*common.Order{}); err != nil {
		t.Errorf("orders for the current turn should be accepted: %s", err)
	}
}

func TestOrdersBeforeStartAreRejected(t *testing.T) {
	DevMode = true

	mapdata, err := common.LoadMap("../maps/tiny.txt")
	if err != nil {
		t.Fatal(err)
	}

	session := NewGameSession("test", 2, "tiny", mapdata)
	player := session.AddPlayer("team")

	if err := session.SetOrders(player.ID, 0, []*common.Order{}); err == nil {
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

func TestMinTurnDuration(t *testing.T) {
	session, players := newTestSession(t, 2)

	DevMode = false
	t.Cleanup(func() { DevMode = true })

	start := time.Now()
	for _, player := range players {
		if err := session.SetOrders(player.ID, 0, []*common.Order{}); err != nil {
			t.Fatal(err)
		}
	}
	if time.Since(start) > MinTurnDuration/2 {
		t.Error("posting orders should not block until the turn is processed")
	}
	if session.FullState().Turn != 0 {
		t.Error("turn should not be processed before the minimum turn duration")
	}

	time.Sleep(MinTurnDuration + 100*time.Millisecond)
	if session.FullState().Turn != 1 {
		t.Error("turn should be processed after the minimum turn duration")
	}
}

func TestPersist(t *testing.T) {
	session, _ := newTestSession(t, 2)

	if err := session.persist(); err != nil {
		t.Fatal(err)
	}

	files, _ := filepath.Glob(filepath.Join(HistoryDir, "*-test-tiny.json"))
	if len(files) != 1 {
		t.Fatalf("expected one history file, got %v", files)
	}

	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var game common.PersistedGame
	if err := json.Unmarshal(data, &game); err != nil {
		t.Fatal(err)
	}
	if game.Id != "test" || len(game.Players) != 2 || len(game.History) != 1 {
		t.Errorf("unexpected history content: %+v", game)
	}

	// A file in place of the history directory
	HistoryDir = files[0]
	if err := session.persist(); err == nil {
		t.Error("expected an error when the history directory cannot be created")
	}
}
