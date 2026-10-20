package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gorilla/websocket"

	"github.com/hivehelsinki/hive-arena/arena"
)

type WebSocketMessage struct {
	Turn     int
	GameOver bool
}

func parseJSON(bytes []byte) *arena.PersistedGame {
	var game arena.PersistedGame
	err := json.Unmarshal(bytes, &game)

	if err != nil {
		fmt.Println(err)
		return nil
	}
	return &game
}

func GetURL(url string) *arena.PersistedGame {
	res, err := http.Get(url)
	if err != nil {
		fmt.Println(err)
		return nil
	}

	body, err := io.ReadAll(res.Body)
	res.Body.Close()

	if err != nil {
		fmt.Println(err)
		return nil
	}

	if res.StatusCode > 299 {
		fmt.Printf("Response failed with status code: %d and\nbody: %s\n", res.StatusCode, body)
		return nil
	}

	return parseJSON(body)
}

func GetFile(path string) *arena.PersistedGame {
	bytes, err := os.ReadFile(path)
	if err != nil {
		fmt.Println(err)
		return nil
	}

	return parseJSON(bytes)
}

func request(url string) (string, error) {

	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	body := string(bodyBytes)

	if resp.StatusCode != 200 {
		return body, fmt.Errorf("Status code %d", resp.StatusCode)
	}

	return body, nil
}

func getState(host string, id string, token string) *arena.GameState {

	url := fmt.Sprintf("http://%s/game?id=%s&token=%s", host, id, token)
	body, err := request(url)

	if err != nil {
		fmt.Println(err, body)
		return nil
	}

	var response arena.GameState
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		fmt.Println("Invalid game state:", err)
		return nil
	}

	return &response
}

func getGameInfo(host string, id string) *arena.SessionStatus {
	url := fmt.Sprintf("http://%s/status", host)
	body, err := request(url)

	if err != nil {
		fmt.Println(err, body)
		return nil
	}

	var response arena.StatusResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		fmt.Println("Invalid status:", err)
		return nil
	}

	for _, status := range response.Games {
		if status.Id == id {
			return &status
		}
	}
	return nil
}

// A new state of a live game, and the game information once all players have joined
type LiveUpdate struct {
	State *arena.GameState
	Info  *arena.SessionStatus
}

// StartLiveWatch follows a live game in the background, and sends its new states to the returned channel
func StartLiveWatch(host string, gameId string, token string) (*arena.PersistedGame, <-chan LiveUpdate) {

	url := fmt.Sprintf("ws://%s/ws?id=%s", host, gameId)
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if ws == nil {
		fmt.Printf("Could not get websocket for %s on %s\n", gameId, host)
		fmt.Println(err)
		return nil, nil
	}

	updates := make(chan LiveUpdate)

	updateGame := func() {
		defer ws.Close()
		hasInfo := false

		for {
			var message WebSocketMessage
			err := ws.ReadJSON(&message)
			if err != nil {
				fmt.Println("Websocket error:", err)
				return
			}

			fmt.Printf("Turn %d\n", message.Turn)

			state := getState(host, gameId, token)
			if state == nil {
				continue
			}

			update := LiveUpdate{State: state}
			if !hasInfo {
				info := getGameInfo(host, gameId)
				if info != nil && len(info.Players) == info.NumPlayers {
					update.Info = info
					hasInfo = true
				}
			}
			updates <- update

			if message.GameOver {
				return
			}
		}
	}

	go updateGame()

	return &arena.PersistedGame{Id: gameId}, updates
}
