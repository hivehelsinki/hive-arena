package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hivehelsinki/hive-arena/arena"
)

// A minimal client for the arena server API
type ArenaClient struct {
	Base string // e.g. http://127.0.0.1:1234
	http http.Client
}

func NewArenaClient(base string) *ArenaClient {
	return &ArenaClient{Base: base, http: http.Client{Timeout: 10 * time.Second}}
}

type httpError struct {
	Status  int
	Message string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
}

// request sends a request and returns the body of a successful response
func (c *ArenaClient) request(ctx context.Context, method string, route string, query url.Values, body string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Base+route+"?"+query.Encode(), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, &httpError{res.StatusCode, strings.TrimSpace(string(data))}
	}
	return data, nil
}

func (c *ArenaClient) get(ctx context.Context, route string, query url.Values, result any) error {
	data, err := c.request(ctx, "GET", route, query, "")
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

type NewGameResponse struct {
	Id         string `json:"id"`
	AdminToken string `json:"adminToken"`
}

func (c *ArenaClient) NewGame(ctx context.Context, mapname string, players int) (*NewGameResponse, error) {
	var game NewGameResponse
	err := c.get(ctx, "/newgame", url.Values{"map": {mapname}, "players": {fmt.Sprint(players)}}, &game)
	return &game, err
}

// GameStatus returns the status of a game, or nil if the server does not know it
func (c *ArenaClient) GameStatus(ctx context.Context, id string) (*arena.SessionStatus, error) {
	var status arena.StatusResponse
	if err := c.get(ctx, "/status", nil, &status); err != nil {
		return nil, err
	}
	for _, game := range status.Games {
		if game.Id == id {
			return &game, nil
		}
	}
	return nil, nil
}

// Report returns the raw report of a finished game, or nil if it is not over yet
func (c *ArenaClient) Report(ctx context.Context, id string) ([]byte, error) {
	data, err := c.request(ctx, "GET", "/report", url.Values{"id": {id}}, "")
	if herr, ok := err.(*httpError); ok && herr.Status == http.StatusBadRequest {
		return nil, nil
	}
	return data, err
}

type JoinResponse struct {
	Id    int    `json:"id"`
	Token string `json:"token"`
}

func (c *ArenaClient) Join(ctx context.Context, id string, name string) (*JoinResponse, error) {
	var player JoinResponse
	err := c.get(ctx, "/join", url.Values{"id": {id}, "name": {name}}, &player)
	return &player, err
}

func (c *ArenaClient) Game(ctx context.Context, id string, token string) (*arena.GameState, error) {
	var state arena.GameState
	err := c.get(ctx, "/game", url.Values{"id": {id}, "token": {token}}, &state)
	return &state, err
}

func (c *ArenaClient) PostOrders(ctx context.Context, id string, token string, turn uint, orders string) error {
	_, err := c.request(ctx, "POST", "/orders", url.Values{"id": {id}, "token": {token}, "turn": {fmt.Sprint(turn)}}, orders)
	return err
}

func (c *ArenaClient) Forfeit(ctx context.Context, id string, adminToken string, player int) error {
	_, err := c.request(ctx, "GET", "/forfeit", url.Values{"id": {id}, "token": {adminToken}, "player": {fmt.Sprint(player)}}, "")
	return err
}
