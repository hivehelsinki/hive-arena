package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hivehelsinki/hive-arena/common"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	DevMode = true
	HistoryDir = t.TempDir()

	mapdata, err := common.LoadMap("../maps/tiny.txt")
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{
		Maps:     map[string]common.MapData{"tiny": mapdata},
		Sessions: make(map[string]*GameSession),
	}

	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer
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

	var status common.StatusResponse
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

	var full, view common.GameState
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

	var state common.GameState
	call(t, server, "GET", "/game?id="+game.Id+"&token="+game.AdminToken, "", http.StatusOK, &state)
	if state.Turn != 1 {
		t.Errorf("expected the turn to be processed once all players posted, current turn is %d", state.Turn)
	}

	call(t, server, "POST", route(0, "0"), "[]", http.StatusBadRequest, nil)
}
