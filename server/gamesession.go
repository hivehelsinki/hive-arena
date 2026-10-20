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

	"github.com/hivehelsinki/hive-arena/common"
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
	State        *common.GameState

	PendingOrders [][]*common.Order
	Posted        []bool
	TurnStart     time.Time
	History       []common.Turn

	Sockets []*Socket
}

func generateTokens(count int) []string {
	tokens := make(map[string]bool)

	for len(tokens) < count {
		tokens[fmt.Sprintf("%x", rand.Uint64())] = true
	}

	return slices.Collect(maps.Keys(tokens))
}

func NewGameSession(id string, players int, mapname string, mapdata common.MapData) *GameSession {

	tokens := generateTokens(players + 1)
	state := common.NewGameState(mapdata, players)

	return &GameSession{
		ID:           id,
		Map:          mapname,
		CreatedDate:  time.Now(),
		AdminToken:   tokens[0],
		PlayerTokens: tokens[1:],
		State:        state,
		History:      []common.Turn{{Orders: nil, State: state.Clone()}},
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
func (session *GameSession) FullState() *common.GameState {
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

func (session *GameSession) GetView(token string) *common.GameState {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	playerid := slices.Index(session.PlayerTokens, token)
	if playerid < 0 || playerid >= len(session.Players) {
		return nil
	}

	return session.State.PlayerView(playerid)
}

func (session *GameSession) BeginTurn() {
	session.notifySockets()

	if session.State.GameOver {
		return
	}

	session.PendingOrders = make([][]*common.Order, session.State.NumPlayers)
	session.Posted = make([]bool, session.State.NumPlayers)
	session.TurnStart = time.Now()

	session.processTurnAfter(TurnTimeout)
}

// processTurnAfter processes the current turn after a delay, unless it has been processed in the meantime
func (session *GameSession) processTurnAfter(delay time.Duration) {
	currentTurn := session.State.Turn
	time.AfterFunc(delay, func() {
		session.mutex.Lock()
		defer session.mutex.Unlock()

		if session.State.Turn == currentTurn && !session.State.GameOver {
			session.processTurn()
		}
	})
}

func (session *GameSession) SetOrders(playerid int, turn uint, orders []*common.Order) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	if !session.isFull() {
		return fmt.Errorf("Game has not started")
	}
	if session.State.GameOver {
		return fmt.Errorf("Game is over")
	}
	if turn != session.State.Turn {
		return fmt.Errorf("Orders are for turn %d, but the current turn is %d", turn, session.State.Turn)
	}

	session.PendingOrders[playerid] = orders
	session.Posted[playerid] = true

	log.Printf("Player %s posted orders in game %s", session.Players[playerid].Name, session.ID)

	if session.allPlayed() {
		// Turns last at least MinTurnDuration, so that games can be followed live
		delay := MinTurnDuration - time.Since(session.TurnStart)
		if DevMode || delay <= 0 {
			session.processTurn()
		} else {
			session.processTurnAfter(delay)
		}
	}

	return nil
}

func (session *GameSession) allPlayed() bool {
	return !slices.Contains(session.Posted, false)
}

func (session *GameSession) processTurn() {
	log.Printf("Processing orders for game %s, turn %d", session.ID, session.State.Turn)

	results, err := session.State.ProcessOrders(session.PendingOrders)
	if err != nil {
		log.Printf("Could not process orders for game %s: %s", session.ID, err)
		return
	}
	session.History = append(session.History, common.Turn{Orders: results, State: session.State.Clone()})

	if session.State.GameOver {
		log.Printf("Game %s is over", session.ID)
		if err := session.persist(); err != nil {
			log.Printf("Could not save the history of game %s: %s", session.ID, err)
		}
	}

	session.BeginTurn()
}

func (session *GameSession) RegisterWebSocket(conn *websocket.Conn) {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	socket := NewSocket(conn, func(socket *Socket) {
		session.mutex.Lock()
		defer session.mutex.Unlock()

		session.closeSocket(socket)
	})
	session.Sockets = append(session.Sockets, socket)

	if session.isFull() {
		session.notifySocket(socket)
	}
}

func (session *GameSession) notifySocket(socket *Socket) {
	message, _ := json.Marshal(map[string]any{
		"turn":     session.State.Turn,
		"gameOver": session.State.GameOver,
	})

	if !socket.Send(message) {
		log.Printf("Closing a websocket of game %s that is not keeping up", session.ID)
		session.closeSocket(socket)
	} else if session.State.GameOver {
		session.closeSocket(socket)
	}
}

func (session *GameSession) notifySockets() {
	for _, socket := range slices.Clone(session.Sockets) {
		session.notifySocket(socket)
	}
}

// closeSocket removes a socket from the game, and closes it after its pending messages are sent
func (session *GameSession) closeSocket(socket *Socket) {
	index := slices.Index(session.Sockets, socket)
	if index < 0 {
		return
	}

	session.Sockets = slices.Delete(session.Sockets, index, index+1)
	socket.Close()
}

func (session *GameSession) CloseSockets() {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	for _, socket := range slices.Clone(session.Sockets) {
		session.closeSocket(socket)
	}
}

func (session *GameSession) persist() error {
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

	info := common.PersistedGame{
		Id:          session.ID,
		Map:         session.Map,
		CreatedDate: session.CreatedDate,
		Players:     players,
		History:     session.History,
	}

	if err := os.MkdirAll(HistoryDir, 0o755); err != nil {
		return err
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}

	if err := json.NewEncoder(file).Encode(info); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func (session *GameSession) Status() common.SessionStatus {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	var players []string
	for _, player := range session.Players {
		players = append(players, player.Name)
	}

	return common.SessionStatus{
		Id:          session.ID,
		CreatedDate: session.CreatedDate,
		Map:         session.Map,
		NumPlayers:  session.State.NumPlayers,
		Players:     players,
		GameOver:    session.State.GameOver,
	}
}
