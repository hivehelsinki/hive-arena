package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/gorilla/websocket"

	. "hive-arena/common"
)

func request(url string) string {

	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("Could not get " + url)
		os.Exit(1)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	body := string(bodyBytes)

	if resp.StatusCode != 200 {
		fmt.Println("Error:", body)
		os.Exit(1)
	}

	return body
}

type JoinResponse struct {
	Id    int
	Token string
}

type WebSocketMessage struct {
	Turn     uint
	GameOver bool
}

func joinGame(host string, id string, name string) JoinResponse {

	query := url.Values{"id": {id}, "name": {name}}
	body := request("http://" + host + "/join?" + query.Encode())

	var response JoinResponse
	json.Unmarshal([]byte(body), &response)

	fmt.Printf("Joined game %s as player %d\n", id, response.Id)

	return response
}

func startWebSocket(host string, id string) *websocket.Conn {

	url := "ws://" + host + fmt.Sprintf("/ws?id=%s", id)

	ws, _, err := websocket.DefaultDialer.Dial(url, nil)

	if err != nil {
		fmt.Println("Websocket error: ", err)
		os.Exit(1)
	}

	return ws
}

func getState(host string, id string, token string) GameState {

	url := "http://" + host + fmt.Sprintf("/game?id=%s&token=%s", id, token)
	body := request(url)

	var response GameState
	json.Unmarshal([]byte(body), &response)

	return response
}

func sendOrders(host string, id string, token string, turn uint, orders []Order) {
	url := "http://" + host + fmt.Sprintf("/orders?id=%s&token=%s&turn=%d", id, token, turn)
	payload, err := json.Marshal(orders)
	if err != nil {
		fmt.Println("Could not encode orders:", err)
		os.Exit(1)
	}

	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		fmt.Println("Could not post to " + url)
		os.Exit(1)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	body := string(bodyBytes)

	if resp.StatusCode != 200 {
		fmt.Println("Error:", body)
		os.Exit(1)
	}
}

func Run(host string, id string, name string, callback func(*GameState, int) []Order) {

	playerInfo := joinGame(host, id, name)
	ws := startWebSocket(host, id)
	lastPlayedTurn := -1

	run := func() {
		state := getState(host, id, playerInfo.Token)
		lastPlayedTurn = int(state.Turn)

		orders := callback(&state, playerInfo.Id)
		sendOrders(host, id, playerInfo.Token, state.Turn, orders)
	}

	for {
		var message WebSocketMessage
		err := ws.ReadJSON(&message)
		if err != nil {
			fmt.Println("Error:", err)
			os.Exit(1)
		}

		if message.GameOver {
			fmt.Println("Game is over")
			break
		} else if int(message.Turn) > lastPlayedTurn {
			fmt.Printf("Starting turn %d\n", message.Turn)
			run()
		}
	}
}
