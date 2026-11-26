package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

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

// The states of the live manual match of the ladder, from its /live/states route
type ladderLiveStates struct {
	GameID      string            `json:"gameId"`
	Map         string            `json:"map"`
	CreatedDate time.Time         `json:"createdDate"`
	Players     []string          `json:"players"`
	From        int               `json:"from"`
	States      []json.RawMessage `json:"states"`
	Over        bool              `json:"over"`
}

// StartLadderLive follows a live game of the ladder in the background, by
// asking for its new states regularly (url: its /live/states address,
// with the game), and sends them to the returned channel. It works in the
// browser, where the server's websocket is not available.
func StartLadderLive(url string) (*arena.PersistedGame, <-chan LiveUpdate) {
	updates := make(chan LiveUpdate)
	separator := "?"
	if strings.Contains(url, "?") {
		separator = "&"
	}

	go func() {
		next := 0
		hasInfo := false
		for {
			res, err := http.Get(fmt.Sprintf("%s%sfrom=%d", url, separator, next))
			if err != nil {
				fmt.Println(err)
				time.Sleep(2 * time.Second)
				continue
			}
			var live ladderLiveStates
			err = json.NewDecoder(res.Body).Decode(&live)
			status := res.StatusCode
			res.Body.Close()
			if status == http.StatusNotFound {
				fmt.Println("The live game is over, or was replaced by another one")
				return
			}
			if err != nil || status != http.StatusOK {
				fmt.Println("Live states:", status, err)
				time.Sleep(2 * time.Second)
				continue
			}

			for _, data := range live.States {
				var state arena.GameState
				if err := json.Unmarshal(data, &state); err != nil {
					fmt.Println("Invalid game state:", err)
					continue
				}
				update := LiveUpdate{State: &state}
				if !hasInfo && len(live.Players) > 0 {
					update.Info = &arena.SessionStatus{Id: live.GameID, Map: live.Map, CreatedDate: live.CreatedDate,
						NumPlayers: len(live.Players), Players: live.Players}
					hasInfo = true
				}
				updates <- update
			}
			next = live.From + len(live.States)
			if live.Over && len(live.States) == 0 {
				return
			}
			// Turns last at least half a second
			time.Sleep(250 * time.Millisecond)
		}
	}()

	return &arena.PersistedGame{}, updates
}
