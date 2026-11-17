package main

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hivehelsinki/hive-arena/arena"
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
	Seats        []*Player // indexed by player ID, nil for seats not taken yet
	joined       int
	State        *arena.GameState

	PendingOrders [][]*arena.Order
	Posted        []bool
	Forfeited     []bool // players the server no longer waits for
	TurnStart     time.Time
	History       []arena.Turn

	Sockets []*Socket

	// Reserved seats: private game IDs, each joining once under its name.
	// Games with reserved seats cannot be joined with their public ID.
	JoinIDs    map[string]string // private game ID -> name
	usedJoinID map[string]bool
}

func generateTokens(count int) []string {
	tokens := make(map[string]bool)

	for len(tokens) < count {
		tokens[fmt.Sprintf("%x", rand.Uint64())] = true
	}

	return slices.Collect(maps.Keys(tokens))
}

func NewGameSession(id string, players int, mapname string, mapdata arena.MapData) *GameSession {

	tokens := generateTokens(players + 1)
	state := arena.NewGameState(mapdata, players)

	return &GameSession{
		ID:           id,
		Map:          mapname,
		CreatedDate:  time.Now(),
		AdminToken:   tokens[0],
		PlayerTokens: tokens[1:],
		Seats:        make([]*Player, players),
		Forfeited:    make([]bool, players),
		State:        state,
		History:      []arena.Turn{{Orders: nil, State: state.Clone()}},
	}
}

func (session *GameSession) isFull() bool {
	return session.joined == session.State.NumPlayers
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
func (session *GameSession) FullState() *arena.GameState {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	return session.State.Clone()
}

// AddPlayer seats a player who joined with the public game ID
func (session *GameSession) AddPlayer(name string) (*Player, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	if session.JoinIDs != nil {
		return nil, fmt.Errorf("This game has reserved seats: join with your own game ID")
	}
	return session.addPlayer(name)
}

// AddReservedPlayer seats the player of a reserved seat, under the name of the
// seat. Each seat can only be taken once.
func (session *GameSession) AddReservedPlayer(joinID string) (*Player, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	name, ok := session.JoinIDs[joinID]
	if !ok {
		return nil, fmt.Errorf("Invalid game id: %s", joinID)
	}
	if session.usedJoinID[joinID] {
		return nil, fmt.Errorf("This seat is already taken")
	}
	player, err := session.addPlayer(name)
	if err == nil {
		session.usedJoinID[joinID] = true
	}
	return player, err
}

func (session *GameSession) addPlayer(name string) (*Player, error) {
	if session.isFull() {
		return nil, fmt.Errorf("Game is full")
	}

	// Seats are assigned at random, so that the join order does not decide
	// who plays from which starting position
	var free []int
	for seat, player := range session.Seats {
		if player == nil {
			free = append(free, seat)
		}
	}
	id := free[rand.Intn(len(free))]
	player := Player{id, name, session.PlayerTokens[id]}

	session.Seats[id] = &player
	session.joined++

	if session.isFull() {
		session.BeginTurn()
	}

	return &player, nil
}

// ReserveSeats makes the game joinable only through one private game ID per
// name, and returns them (name -> ID)
func (session *GameSession) ReserveSeats(names []string) map[string]string {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	session.JoinIDs = make(map[string]string)
	session.usedJoinID = make(map[string]bool)
	ids := make(map[string]string)
	for _, name := range names {
		joinID := fmt.Sprintf("%s-%x", session.ID, rand.Uint64())
		for session.JoinIDs[joinID] != "" {
			joinID = fmt.Sprintf("%s-%x", session.ID, rand.Uint64())
		}
		session.JoinIDs[joinID] = name
		ids[name] = joinID
	}
	return ids
}

func (session *GameSession) Player(token string) (Player, bool) {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	playerid := session.seatOf(token)
	if playerid < 0 {
		return Player{}, false
	}
	return *session.Seats[playerid], true
}

func (session *GameSession) GetView(token string) *arena.GameState {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	playerid := session.seatOf(token)
	if playerid < 0 {
		return nil
	}

	return session.State.PlayerView(playerid)
}

// seatOf returns the player ID of a player who joined with the given token, or -1
func (session *GameSession) seatOf(token string) int {
	playerid := slices.Index(session.PlayerTokens, token)
	if playerid < 0 || session.Seats[playerid] == nil {
		return -1
	}
	return playerid
}

func (session *GameSession) BeginTurn() {
	session.notifySockets()

	if session.State.GameOver {
		return
	}

	session.PendingOrders = make([][]*arena.Order, session.State.NumPlayers)
	session.Posted = make([]bool, session.State.NumPlayers)
	session.TurnStart = time.Now()

	if session.allPlayed() {
		// Everyone has forfeited: no orders will come
		session.processTurnAfter(session.remainingMinDuration())
		return
	}
	session.processTurnAfter(TurnTimeout)
}

// remainingMinDuration is the time left before the current turn has lasted
// its minimum duration (always 0 in dev mode)
func (session *GameSession) remainingMinDuration() time.Duration {
	if DevMode {
		return 0
	}
	return max(0, MinTurnDuration-time.Since(session.TurnStart))
}

// processIfAllPlayed processes the turn once all players have posted their
// orders (or forfeited), but not before the minimum turn duration, so that
// games can be followed live
func (session *GameSession) processIfAllPlayed() {
	if !session.allPlayed() {
		return
	}
	if delay := session.remainingMinDuration(); delay > 0 {
		session.processTurnAfter(delay)
	} else {
		session.processTurn()
	}
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

func (session *GameSession) SetOrders(playerid int, turn uint, orders []*arena.Order) error {
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
	if limit := session.maxOrders(playerid); len(orders) > limit {
		return fmt.Errorf("Too many orders: %d, the limit is %d this turn", len(orders), limit)
	}

	session.PendingOrders[playerid] = orders
	session.Posted[playerid] = true

	log.Printf("Player %s posted orders in game %s", session.Seats[playerid].Name, session.ID)

	session.processIfAllPlayed()
	return nil
}

// maxOrders is the maximum number of orders a player can post in the current
// turn: each unit acts at most once, and hives can spawn bees that act in the
// same turn. Orders beyond that could only fail.
func (session *GameSession) maxOrders(playerid int) int {
	units := 0
	for _, hex := range session.State.Hexes {
		if hex.Entity != nil && hex.Entity.Player == playerid && hex.Entity.Type != arena.WALL {
			units++
		}
	}
	return 2*units + 16
}

// Forfeit stops waiting for a player's orders for the rest of the game, for
// instance because its agent crashed. The player stays in the game, its units
// just get no orders.
func (session *GameSession) Forfeit(playerid int) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	if !session.isFull() {
		return fmt.Errorf("Game has not started")
	}
	if session.State.GameOver {
		return fmt.Errorf("Game is over")
	}
	if playerid < 0 || playerid >= session.State.NumPlayers {
		return fmt.Errorf("Invalid player: %d", playerid)
	}

	session.Forfeited[playerid] = true
	log.Printf("Player %s forfeited in game %s", session.Seats[playerid].Name, session.ID)

	session.processIfAllPlayed()
	return nil
}

func (session *GameSession) allPlayed() bool {
	for player, posted := range session.Posted {
		if !posted && !session.Forfeited[player] {
			return false
		}
	}
	return true
}

func (session *GameSession) processTurn() {
	log.Printf("Processing orders for game %s, turn %d", session.ID, session.State.Turn)

	results, err := session.State.ProcessOrders(session.PendingOrders)
	if err != nil {
		log.Printf("Could not process orders for game %s: %s", session.ID, err)
		return
	}
	session.History = append(session.History, arena.Turn{Orders: results, State: session.State.Clone()})

	if session.State.GameOver {
		log.Printf("Game %s is over", session.ID)
		if HistoryDir != "" {
			if err := session.persist(); err != nil {
				log.Printf("Could not save the history of game %s: %s", session.ID, err)
			}
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

// playerNames returns the names of the players who joined, ordered by player ID
func (session *GameSession) playerNames() []string {
	var names []string
	for _, player := range session.Seats {
		if player != nil {
			names = append(names, player.Name)
		}
	}
	return names
}

// result returns the final result of the game, or nil if it is not over
func (session *GameSession) result() *arena.GameResult {
	if !session.State.GameOver {
		return nil
	}
	result := arena.NewGameResult(session.State, session.playerNames())
	for player, forfeited := range session.Forfeited {
		result.Players[player].Forfeited = forfeited
	}
	return result
}

func (session *GameSession) report() *arena.PersistedGame {
	return &arena.PersistedGame{
		Id:          session.ID,
		Map:         session.Map,
		CreatedDate: session.CreatedDate,
		Players:     session.playerNames(),
		History:     session.History,
		Result:      session.result(),
	}
}

// Report returns the full report of the game, or nil if it is not over
func (session *GameSession) Report() *arena.PersistedGame {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	if !session.State.GameOver {
		return nil
	}
	return session.report()
}

func (session *GameSession) persist() error {
	date, _ := session.CreatedDate.MarshalText()
	path := filepath.Join(HistoryDir, fmt.Sprintf("%s-%s-%s.json",
		date,
		session.ID,
		session.Map,
	))

	if err := os.MkdirAll(HistoryDir, 0o755); err != nil {
		return err
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}

	if err := session.report().WriteJSON(file); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func (session *GameSession) Status() arena.SessionStatus {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	return arena.SessionStatus{
		Id:          session.ID,
		CreatedDate: session.CreatedDate,
		Map:         session.Map,
		NumPlayers:  session.State.NumPlayers,
		Players:     session.playerNames(),
		GameOver:    session.State.GameOver,
		Result:      session.result(),
	}
}
