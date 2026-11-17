package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hivehelsinki/hive-arena/arena"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	httpServer, _ := newTestServerWithState(t)
	return httpServer
}

func newTestServerWithState(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()

	DevMode = true
	HistoryDir = t.TempDir()

	mapdata, err := arena.LoadMap("../maps/tiny.txt")
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{
		Maps:     map[string]arena.MapData{"tiny": mapdata},
		Sessions: make(map[string]*GameSession),
		JoinIDs:  make(map[string]*GameSession),
	}

	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer, server
}

// call sends a request, checks its status code, and decodes the JSON response into result (if not nil)
func call(t *testing.T, server *httptest.Server, method string, route string, body string, status int, result any) {
	t.Helper()

	req, err := http.NewRequest(method, server.URL+route, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != status {
		var message any
		json.NewDecoder(res.Body).Decode(&message)
		t.Fatalf("%s %s: expected status %d, got %d (%v)", method, route, status, res.StatusCode, message)
	}

	if result != nil {
		if err := json.NewDecoder(res.Body).Decode(result); err != nil {
			t.Fatalf("%s %s: invalid JSON response: %s", method, route, err)
		}
	}
}

type testGame struct {
	Id         string
	AdminToken string
	Players    []struct {
		Id    int
		Token string
	}
}

func startTestGame(t *testing.T, server *httptest.Server, players int) testGame {
	t.Helper()

	var game testGame
	call(t, server, "GET", fmt.Sprintf("/newgame?map=tiny&players=%d", players), "", http.StatusOK, &game)

	game.Players = make([]struct {
		Id    int
		Token string
	}, players)
	for i := range players {
		call(t, server, "GET", fmt.Sprintf("/join?id=%s&name=team%d", game.Id, i), "", http.StatusOK, &game.Players[i])
	}
	return game
}

func TestNewGame(t *testing.T) {
	server := newTestServer(t)

	call(t, server, "GET", "/newgame?map=nope&players=2", "", http.StatusBadRequest, nil)
	call(t, server, "GET", "/newgame?map=tiny&players=7", "", http.StatusBadRequest, nil)
	call(t, server, "GET", "/newgame?map=tiny&players=x", "", http.StatusBadRequest, nil)

	var game map[string]any
	call(t, server, "GET", "/newgame?map=tiny&players=2", "", http.StatusOK, &game)
	if game["id"] == "" || game["adminToken"] == "" || game["numPlayers"] != 2.0 {
		t.Errorf("unexpected response: %v", game)
	}

	var status arena.StatusResponse
	call(t, server, "GET", "/status", "", http.StatusOK, &status)
	if len(status.Games) != 1 || status.Games[0].Id != game["id"] {
		t.Errorf("expected the new game in the status, got %+v", status)
	}
}

func TestJoin(t *testing.T) {
	server := newTestServer(t)

	call(t, server, "GET", "/join?id=nope&name=team", "", http.StatusBadRequest, nil)

	var game testGame
	call(t, server, "GET", "/newgame?map=tiny&players=1", "", http.StatusOK, &game)

	call(t, server, "GET", "/game?id="+game.Id+"&token="+game.AdminToken, "", http.StatusBadRequest, nil)
	call(t, server, "GET", "/join?id="+game.Id+"&name=", "", http.StatusBadRequest, nil)

	var player struct {
		Id    int
		Token string
	}
	call(t, server, "GET", "/join?id="+game.Id+"&name=team", "", http.StatusOK, &player)
	if player.Id != 0 || player.Token == "" {
		t.Errorf("unexpected response: %+v", player)
	}

	call(t, server, "GET", "/join?id="+game.Id+"&name=late", "", http.StatusBadRequest, nil)
}

func TestGameViews(t *testing.T) {
	server := newTestServer(t)
	game := startTestGame(t, server, 2)

	call(t, server, "GET", "/game?id="+game.Id+"&token=nope", "", http.StatusForbidden, nil)

	var full, view arena.GameState
	call(t, server, "GET", "/game?id="+game.Id+"&token="+game.AdminToken, "", http.StatusOK, &full)
	call(t, server, "GET", "/game?id="+game.Id+"&token="+game.Players[0].Token, "", http.StatusOK, &view)

	if len(view.Hexes) == 0 || len(view.Hexes) >= len(full.Hexes) {
		t.Errorf("expected the player view to be a part of the full state, got %d and %d hexes", len(view.Hexes), len(full.Hexes))
	}
}

func TestPostOrders(t *testing.T) {
	server := newTestServer(t)
	game := startTestGame(t, server, 2)

	route := func(player int, turn string) string {
		return fmt.Sprintf("/orders?id=%s&token=%s&turn=%s", game.Id, game.Players[player].Token, turn)
	}

	call(t, server, "POST", fmt.Sprintf("/orders?id=%s&token=nope&turn=0", game.Id), "[]", http.StatusForbidden, nil)
	call(t, server, "POST", route(0, ""), "[]", http.StatusBadRequest, nil)
	call(t, server, "POST", route(0, "1"), "[]", http.StatusBadRequest, nil)
	call(t, server, "POST", route(0, "0"), "{", http.StatusBadRequest, nil)
	call(t, server, "POST", route(0, "0"), "[null]", http.StatusBadRequest, nil)

	call(t, server, "POST", route(0, "0"), `[{"type": "MOVE", "coords": "0,0", "direction": "E"}]`, http.StatusOK, nil)
	call(t, server, "POST", route(1, "0"), "null", http.StatusOK, nil)

	var state arena.GameState
	call(t, server, "GET", "/game?id="+game.Id+"&token="+game.AdminToken, "", http.StatusOK, &state)
	if state.Turn != 1 {
		t.Errorf("expected the turn to be processed once all players posted, current turn is %d", state.Turn)
	}

	call(t, server, "POST", route(0, "0"), "[]", http.StatusBadRequest, nil)
}

func TestRandomSeats(t *testing.T) {
	server := newTestServer(t)

	firstSeats := make(map[int]bool)
	for range 20 {
		game := startTestGame(t, server, 3)

		seats := make(map[int]bool)
		for _, player := range game.Players {
			seats[player.Id] = true
		}
		if len(seats) != 3 || !seats[0] || !seats[1] || !seats[2] {
			t.Fatalf("expected each seat to be taken once, got %+v", game.Players)
		}

		// Each token gives access to the view of its own seat
		var view arena.GameState
		call(t, server, "GET", "/game?id="+game.Id+"&token="+game.Players[0].Token, "", http.StatusOK, &view)

		firstSeats[game.Players[0].Id] = true
	}

	if len(firstSeats) < 2 {
		t.Errorf("expected the first player to join to get different seats, always got %v", firstSeats)
	}
}

// playToEnd posts empty orders for all players until the game is over
func playToEnd(t *testing.T, server *httptest.Server, game testGame) {
	t.Helper()

	for turn := 0; ; turn++ {
		var state arena.GameState
		call(t, server, "GET", "/game?id="+game.Id+"&token="+game.AdminToken, "", http.StatusOK, &state)
		if state.GameOver {
			return
		}
		if turn > 1000 {
			t.Fatal("game did not end")
		}
		for _, player := range game.Players {
			call(t, server, "POST", fmt.Sprintf("/orders?id=%s&token=%s&turn=%d", game.Id, player.Token, state.Turn), "[]", http.StatusOK, nil)
		}
	}
}

func TestReport(t *testing.T) {
	server, state := newTestServerWithState(t)

	call(t, server, "GET", "/report?id=nope", "", http.StatusNotFound, nil)
	call(t, server, "GET", "/report?id=../x", "", http.StatusNotFound, nil)

	game := startTestGame(t, server, 2)
	call(t, server, "GET", "/report?id="+game.Id, "", http.StatusBadRequest, nil)

	playToEnd(t, server, game)

	check := func(report arena.PersistedGame) {
		t.Helper()
		if report.Id != game.Id || len(report.Players) != 2 || len(report.History) < 2 {
			t.Fatalf("unexpected report: %s, %v, %d turns", report.Id, report.Players, len(report.History))
		}
		result := report.Result
		if result == nil || len(result.Players) != 2 || len(result.Winners) == 0 {
			t.Fatalf("unexpected result: %+v", result)
		}
		for seat, player := range game.Players {
			if result.Players[player.Id].Name != fmt.Sprintf("team%d", seat) {
				t.Errorf("result names should be indexed by player ID, got %+v", result.Players)
			}
		}
	}

	// From memory
	var report arena.PersistedGame
	call(t, server, "GET", "/report?id="+game.Id, "", http.StatusOK, &report)
	check(report)

	var status arena.StatusResponse
	call(t, server, "GET", "/status", "", http.StatusOK, &status)
	if len(status.Games) != 1 || status.Games[0].Result == nil {
		t.Errorf("expected the status of a finished game to include its result, got %+v", status)
	}

	// From the history directory, once the game is removed from memory
	state.mutex.Lock()
	delete(state.Sessions, game.Id)
	state.mutex.Unlock()

	report = arena.PersistedGame{}
	call(t, server, "GET", "/report?id="+game.Id, "", http.StatusOK, &report)
	check(report)
}

func TestForfeitRoute(t *testing.T) {
	server := newTestServer(t)
	game := startTestGame(t, server, 2)

	route := func(token string, player string) string {
		return fmt.Sprintf("/forfeit?id=%s&token=%s&player=%s", game.Id, token, player)
	}

	call(t, server, "GET", "/forfeit?id=nope&token=x&player=0", "", http.StatusBadRequest, nil)
	call(t, server, "GET", route(game.Players[0].Token, "1"), "", http.StatusForbidden, nil)
	call(t, server, "GET", route(game.AdminToken, "x"), "", http.StatusBadRequest, nil)
	call(t, server, "GET", route(game.AdminToken, "2"), "", http.StatusBadRequest, nil)

	call(t, server, "GET", route(game.AdminToken, fmt.Sprint(game.Players[1].Id)), "", http.StatusOK, nil)
	call(t, server, "POST", fmt.Sprintf("/orders?id=%s&token=%s&turn=0", game.Id, game.Players[0].Token), "[]", http.StatusOK, nil)

	var state arena.GameState
	call(t, server, "GET", "/game?id="+game.Id+"&token="+game.AdminToken, "", http.StatusOK, &state)
	if state.Turn != 1 {
		t.Errorf("the turn should not wait for the forfeited player, current turn is %d", state.Turn)
	}
}

type reservedGame struct {
	Id         string
	AdminToken string
	JoinIds    map[string]string
}

func TestReservedSeats(t *testing.T) {
	server := newTestServer(t)

	var game reservedGame
	call(t, server, "GET", "/newgame?map=tiny&players=2&names=alpha,beta", "", http.StatusOK, &game)
	if len(game.JoinIds) != 2 || game.JoinIds["alpha"] == "" || game.JoinIds["beta"] == "" || game.JoinIds["alpha"] == game.JoinIds["beta"] {
		t.Fatalf("expected a private game ID per name, got %v", game.JoinIds)
	}

	// The public ID cannot be used to join
	call(t, server, "GET", "/join?id="+game.Id+"&name=alpha", "", http.StatusBadRequest, nil)

	// A private ID joins under its own name, whatever name is given, and only once
	var player struct {
		Id    int
		Token string
	}
	alpha := game.JoinIds["alpha"]
	call(t, server, "GET", "/join?id="+alpha+"&name=beta", "", http.StatusOK, &player)
	call(t, server, "GET", "/join?id="+alpha+"&name=alpha", "", http.StatusBadRequest, nil)

	var status struct {
		Games []struct {
			Id      string
			Players []string
		}
	}
	call(t, server, "GET", "/status", "", http.StatusOK, &status)
	if len(status.Games) != 1 || status.Games[0].Id != game.Id || len(status.Games[0].Players) != 1 || status.Games[0].Players[0] != "alpha" {
		t.Fatalf("expected alpha in the public game, got %+v", status.Games)
	}

	// The private ID works for all the player routes
	call(t, server, "GET", "/join?id="+game.JoinIds["beta"]+"&name=x", "", http.StatusOK, nil)
	call(t, server, "GET", "/game?id="+alpha+"&token="+player.Token, "", http.StatusOK, nil)
	call(t, server, "POST", "/orders?id="+alpha+"&token="+player.Token+"&turn=0", "[]", http.StatusOK, nil)
}

func TestInvalidReservedNames(t *testing.T) {
	server := newTestServer(t)
	for _, names := range []string{"alpha", "alpha,alpha", "alpha,", "a,b,c"} {
		call(t, server, "GET", "/newgame?map=tiny&players=2&names="+names, "", http.StatusBadRequest, nil)
	}
}

func TestMaxGames(t *testing.T) {
	server := newTestServer(t)
	MaxGames = 1
	t.Cleanup(func() { MaxGames = 0 })

	call(t, server, "GET", "/newgame?map=tiny&players=2", "", http.StatusOK, nil)
	call(t, server, "GET", "/newgame?map=tiny&players=2", "", http.StatusForbidden, nil)
}

func TestOrdersBodyLimit(t *testing.T) {
	server := newTestServer(t)
	game := startTestGame(t, server, 2)

	body := "[" + strings.Repeat(" ", MaxBodySize) + "]"
	call(t, server, "POST", fmt.Sprintf("/orders?id=%s&token=%s&turn=0", game.Id, game.Players[0].Token), body, http.StatusBadRequest, nil)
}

func TestNoHistory(t *testing.T) {
	server := newTestServer(t)
	HistoryDir = ""

	call(t, server, "GET", "/report?id=unknown-game", "", http.StatusNotFound, nil)
}
