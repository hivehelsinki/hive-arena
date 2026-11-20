package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hivehelsinki/hive-arena/arena"
)

func newTestSession(t *testing.T, players int) (*GameSession, []Player) {
	t.Helper()

	DevMode = true
	HistoryDir = t.TempDir()

	mapdata := testMap(t)

	session := NewGameSession("test", players, "tiny", mapdata)
	var joined []Player
	for range players {
		player, err := session.AddPlayer("team")
		if err != nil {
			t.Fatal(err)
		}
		joined = append(joined, *player)
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
			if err := session.SetOrders(player.ID, turn, []*arena.Order{}); err != nil {
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

	if err := session.SetOrders(players[0].ID, 1, []*arena.Order{}); err == nil {
		t.Error("orders for a future turn should be rejected")
	}

	for _, player := range players {
		if err := session.SetOrders(player.ID, 0, []*arena.Order{}); err != nil {
			t.Fatal(err)
		}
	}

	if err := session.SetOrders(players[0].ID, 0, []*arena.Order{}); err == nil {
		t.Error("orders for a past turn should be rejected")
	}
	if err := session.SetOrders(players[0].ID, 1, []*arena.Order{}); err != nil {
		t.Errorf("orders for the current turn should be accepted: %s", err)
	}
}

func TestOrdersBeforeStartAreRejected(t *testing.T) {
	DevMode = true

	mapdata := testMap(t)

	session := NewGameSession("test", 2, "tiny", mapdata)
	player, _ := session.AddPlayer("team")

	if err := session.SetOrders(player.ID, 0, []*arena.Order{}); err == nil {
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
		if err := session.SetOrders(player.ID, 0, []*arena.Order{}); err != nil {
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
	var game arena.PersistedGame
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

func TestForfeit(t *testing.T) {
	session, players := newTestSession(t, 3)

	if err := session.Forfeit(5); err == nil {
		t.Error("forfeiting an invalid player should fail")
	}

	// Once a player forfeits, turns no longer wait for it
	if err := session.Forfeit(players[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, player := range players[1:] {
		session.SetOrders(player.ID, 0, []*arena.Order{})
	}
	if turn := session.FullState().Turn; turn != 1 {
		t.Errorf("turn should be processed without the forfeited player, current turn is %d", turn)
	}

	// Forfeiting the last player who has not posted processes the turn
	session.SetOrders(players[1].ID, 1, []*arena.Order{})
	session.Forfeit(players[2].ID)
	if turn := session.FullState().Turn; turn != 2 {
		t.Errorf("turn should be processed when the last player forfeits, current turn is %d", turn)
	}

	// With everyone forfeited, the game plays out by itself
	session.Forfeit(players[1].ID)
	deadline := time.Now().Add(5 * time.Second)
	for !session.IsOver() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !session.IsOver() {
		t.Fatal("the game should end by itself once everyone has forfeited")
	}

	result := session.Report().Result
	for _, player := range result.Players {
		if !player.Forfeited {
			t.Errorf("expected all players to be marked as forfeited: %+v", result.Players)
		}
	}
}

func TestForfeitBeforeStart(t *testing.T) {
	DevMode = true
	mapdata := testMap(t)
	session := NewGameSession("test", 2, "tiny", mapdata)
	player, _ := session.AddPlayer("team")

	if err := session.Forfeit(player.ID); err == nil {
		t.Error("forfeiting before the game starts should fail")
	}
}

func TestTooManyOrdersAreRejected(t *testing.T) {
	session, players := newTestSession(t, 2)

	// Each player starts with a hive and a few bees on tiny
	limit := session.maxOrders(players[0].ID)
	orders := make([]*arena.Order, limit+1)
	for i := range orders {
		orders[i] = &arena.Order{Type: arena.FORAGE}
	}
	if err := session.SetOrders(players[0].ID, 0, orders); err == nil {
		t.Errorf("%d orders should be rejected", len(orders))
	}
	if err := session.SetOrders(players[0].ID, 0, orders[:limit]); err != nil {
		t.Errorf("%d orders should be accepted: %s", limit, err)
	}
}

func TestTimeBudget(t *testing.T) {
	TimeBudget = 5 * time.Second
	defer func() { TimeBudget = 0 }()
	session, players := newTestSession(t, 2)

	fast, slow := players[0].ID, players[1].ID
	// Player 0 answers at once, player 1 never does: each turn costs it the whole turn timeout
	for turn := range uint(3) {
		if err := session.SetOrders(fast, turn, []*arena.Order{}); err != nil {
			t.Fatal(err)
		}
		session.mutex.Lock()
		if turn < 2 && session.TimedOut[slow] {
			t.Errorf("player 1 timed out too early, after %d turns", turn)
		}
		session.processTurn()
		session.mutex.Unlock()
	}

	session.mutex.Lock()
	timedOut := slices.Clone(session.TimedOut)
	forfeited := slices.Clone(session.Forfeited)
	used := slices.Clone(session.Used)
	session.mutex.Unlock()
	if !timedOut[slow] || !forfeited[slow] || timedOut[fast] || forfeited[fast] {
		t.Errorf("expected only player 1 to be timed out: %v %v", timedOut, forfeited)
	}
	if used[fast] > time.Second || used[slow] != 3*TurnTimeout {
		t.Errorf("unexpected time used: %v", used)
	}
	if err := session.SetOrders(slow, 3, []*arena.Order{}); err == nil {
		t.Errorf("orders of a player who used up its budget should be rejected")
	}
	// The server no longer waits for it: player 0 alone completes the turn
	if err := session.SetOrders(fast, 3, []*arena.Order{}); err != nil {
		t.Fatal(err)
	}
	if turn := session.FullState().Turn; turn != 4 {
		t.Errorf("expected turn 4, got %d", turn)
	}
}
