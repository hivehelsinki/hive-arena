package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gorilla/websocket"

	. "hive-arena/common"
)

type WebSocketMessage struct {
	Turn     int
	GameOver bool
}

func parseJSON(bytes []byte) *PersistedGame {
	var game PersistedGame
	err := json.Unmarshal(bytes, &game)

	if err != nil {
		fmt.Println(err)
		return nil
	}
	return &game
}

func GetURL(url string) *PersistedGame {
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

func GetFile(path string) *PersistedGame {
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

func getState(host string, id string, token string) *GameState {

	url := fmt.Sprintf("http://%s/game?id=%s&token=%s", host, id, token)
	body, err := request(url)

	if err != nil {
		fmt.Println(err, body)
		return nil
	}

	var response GameState
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		fmt.Println("Invalid game state:", err)
		return nil
	}

	return &response
}

func fillGameInfo(host string, id string, game *PersistedGame) {
	url := fmt.Sprintf("http://%s/status", host)
	body, err := request(url)

	if err != nil {
		fmt.Println(err, body)
		return
	}

	var response StatusResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		fmt.Println("Invalid status:", err)
		return
	}

	for _, status := range response.Games {
		if status.Id == id {
			game.Players = status.Players
			game.Map = status.Map
			game.CreatedDate = status.CreatedDate
			return
		}
	}
}

type LiveGame struct {
	Host, Id, Token string
	Channel         chan int
}

func StartLiveWatch(host string, gameId string, token string) (*PersistedGame, *LiveGame) {

	url := fmt.Sprintf("ws://%s/ws?id=%s", host, gameId)
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if ws == nil {
		fmt.Printf("Could not get websocket for %s on %s\n", gameId, host)
		fmt.Println(err)
		return nil, nil
	}

	liveChannel := make(chan int)

	updateGame := func() {
		for {
			var message WebSocketMessage
			err := ws.ReadJSON(&message)
			if err != nil {
				fmt.Println("Websocket error:", err)
				return
			}
			liveChannel <- message.Turn
			if message.GameOver {
				break
			}
		}
	}

	go updateGame()

	return &PersistedGame{Id: gameId}, &LiveGame{host, gameId, token, liveChannel}
}
