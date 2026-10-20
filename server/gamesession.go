package main

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"math/rand"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	. "hive-arena/common"
)

const MinTurnDuration = 500 * time.Millisecond
const TurnTimeout = 2 * time.Second

type Player struct {
	ID    int
	Name  string
	Token string
}

type GameSession struct {
	mutex sync.Mutex

	ID           string
	Map          string
	CreatedDate  time.Time
	AdminToken   string
	PlayerTokens []string
	Players      []Player
	State        *GameState

	PendingOrders [][]*Order
	History       []Turn

	Sockets []*websocket.Conn
}

func generateTokens(count int) []string {
	tokens := make(map[string]bool)

	for len(tokens) < count {
		tokens[fmt.Sprintf("%x", rand.Uint64())] = true
	}

	return slices.Collect(maps.Keys(tokens))
}

func NewGameSession(id string, players int, mapname string, mapdata MapData) *GameSession {

	tokens := generateTokens(players + 1)
	state := NewGameState(mapdata, players)

	return &GameSession{
		ID:           id,
		Map:          mapname,
		CreatedDate:  time.Now(),
		AdminToken:   tokens[0],
		PlayerTokens: tokens[1:],
		State:        state,
		History:      []Turn{{Orders: nil, State: state.Clone()}},
	}
}

func (session *GameSession) isFull() bool {
	return len(session.Players) == session.State.NumPlayers
}

// Started reports whether all players have joined
func (session *GameSession) Started() bool {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	return session.isFull()
}

func (session *GameSession) IsOver() bool {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	return session.State.GameOver
}

// FullState returns a copy of the current state, safe to use without holding the lock
func (session *GameSession) FullState() *GameState {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	return session.State.Clone()
}

func (session *GameSession) AddPlayer(name string) *Player {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	if session.isFull() {
		return nil
	}

	id := len(session.Players)
	player := Player{id, name, session.PlayerTokens[id]}

	session.Players = append(session.Players, player)

	if session.isFull() {
		session.BeginTurn()
	}

	return &player
}

func (session *GameSession) Player(token string) (Player, bool) {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	playerid := slices.Index(session.PlayerTokens, token)
	if playerid < 0 || playerid >= len(session.Players) {
		return Player{}, false
	}
	return session.Players[playerid], true
}

func (session *GameSession) GetView(token string) *GameState {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	playerid := slices.Index(session.PlayerTokens, token)
	if playerid < 0 || playerid >= len(session.Players) {
		return nil
	}

	return session.State.PlayerView(playerid)
}

func (session *GameSession) BeginTurn() {

	if !DevMode {
		time.Sleep(MinTurnDuration)
	}

	session.notifySockets()

	if session.State.GameOver {
		return
	}

	session.PendingOrders = make([][]*Order, session.State.NumPlayers)

	currentTurn := session.State.Turn
	time.AfterFunc(TurnTimeout, func() {
		session.mutex.Lock()
		defer session.mutex.Unlock()

		if session.State.Turn == currentTurn {
			session.processTurn()
		}
	})
}

func (session *GameSession) SetOrders(playerid int, orders []*Order) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	if !session.isFull() {
		return fmt.Errorf("Game has not started")
	}
	if session.State.GameOver {
		return fmt.Errorf("Game is over")
	}

	session.PendingOrders[playerid] = orders

	log.Printf("Player %s posted orders in game %s", session.Players[playerid].Name, session.ID)

	if session.allPlayed() {
		session.processTurn()
	}

	return nil
}

func (session *GameSession) allPlayed() bool {
	for _, orders := range session.PendingOrders {
		if orders == nil {
			return false
		}
	}
	return true
}

func (session *GameSession) processTurn() {
	log.Printf("Processing orders for game %s, turn %d", session.ID, session.State.Turn)

	results, _ := session.State.ProcessOrders(session.PendingOrders)
	session.History = append(session.History, Turn{Orders: results, State: session.State.Clone()})

	if session.State.GameOver {
		log.Printf("Game %s is over", session.ID)
		session.persist()
	}

	session.BeginTurn()
}

func (session *GameSession) RegisterWebSocket(socket *websocket.Conn) {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	session.Sockets = append(session.Sockets, socket)

	if session.isFull() {
		session.notifySocket(socket)
	}
}

func (session *GameSession) notifySocket(socket *websocket.Conn) {
	message, _ := json.Marshal(map[string]any{
		"turn":     session.State.Turn,
		"gameOver": session.State.GameOver,
	})

	socket.WriteMessage(websocket.TextMessage, message)

	if session.State.GameOver {
		socket.Close()
	}
}

func (session *GameSession) notifySockets() {
	for _, socket := range session.Sockets {
		session.notifySocket(socket)
	}
}

func (session *GameSession) persist() {
	date, _ := session.CreatedDate.MarshalText()
	path := fmt.Sprintf("%s/%s-%s-%s.json",
		HistoryDir,
		date,
		session.ID,
		session.Map,
	)

	players := make([]string, len(session.Players))
	for i, player := range session.Players {
		players[i] = player.Name
	}

	info := PersistedGame{
		Id:          session.ID,
		Map:         session.Map,
		CreatedDate: session.CreatedDate,
		Players:     players,
		History:     session.History,
	}

	file, _ := os.Create(path)
	defer file.Close()

	json.NewEncoder(file).Encode(info)
}

func (session *GameSession) Status() SessionStatus {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	var players []string
	for _, player := range session.Players {
		players = append(players, player.Name)
	}

	return SessionStatus{
		Id:          session.ID,
		CreatedDate: session.CreatedDate,
		Map:         session.Map,
		NumPlayers:  session.State.NumPlayers,
		Players:     players,
		GameOver:    session.State.GameOver,
	}
}
