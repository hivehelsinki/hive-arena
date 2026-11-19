package main

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hivehelsinki/hive-arena/arena"
)

const GameStartTimeout = 5 * time.Minute

var MapDir = "maps"
var HistoryDir = "history" // empty: reports are not saved

// Maximum number of games the server creates in its lifetime (0: no limit)
var MaxGames = 0

// Maximum size of a request body
const MaxBodySize = 1 << 20

type Server struct {
	mutex sync.Mutex

	Maps     map[string]arena.MapData
	Sessions map[string]*GameSession
	JoinIDs  map[string]*GameSession // private game IDs of reserved seats
	created  int
}

func loadMaps() map[string]arena.MapData {

	data := make(map[string]arena.MapData)

	entries, err := os.ReadDir(MapDir)
	if err != nil {
		log.Fatalf("Could not find maps directory")
	}

	for _, entry := range entries {
		name, isMap := strings.CutSuffix(entry.Name(), ".txt")
		if !isMap || entry.IsDir() {
			continue
		}

		mapdata, err := arena.LoadMap(filepath.Join(MapDir, entry.Name()))
		if err != nil {
			log.Fatalf("Could not load map %s: %s", name, err)
		}

		data[name] = mapdata
	}

	log.Printf("Loaded maps: %s", strings.Join(slices.Collect(maps.Keys(data)), ", "))

	return data
}

// RandomMap is the map name of random maps: "random" (a new one), or
// "random:<seed>" (always the same one)
const RandomMap = "random"

func (server *Server) findMap(name string, players int) (arena.MapData, error) {
	if name == RandomMap || strings.HasPrefix(name, RandomMap+":") {
		seed := rand.Int63()
		if s, ok := strings.CutPrefix(name, RandomMap+":"); ok {
			var err error
			if seed, err = strconv.ParseInt(s, 10, 64); err != nil {
				return arena.MapData{}, fmt.Errorf("invalid random map seed: %s", s)
			}
		}
		mapdata, err := arena.GenerateMap(players, seed, arena.DefaultMapSettings())
		mapdata.Seed = seed
		return mapdata, err
	}
	mapdata, found := server.Maps[name]
	if !found {
		return arena.MapData{}, fmt.Errorf("Map not found: %s", name)
	}
	return mapdata, nil
}

func logRoute(r *http.Request) {
	log.Printf("%s %v %v", r.Method, r.URL, r.RemoteAddr)
}

func writeJson(w http.ResponseWriter, payload any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

func (server *Server) handleNewGame(w http.ResponseWriter, r *http.Request) {

	logRoute(r)

	playerStr := r.URL.Query().Get("players")
	players, ok := strconv.Atoi(playerStr)
	if ok != nil || !arena.IsValidNumPlayers(players) {
		writeJson(w, "Invalid number of players: "+playerStr, http.StatusBadRequest)
		return
	}

	mapname := r.URL.Query().Get("map")
	mapdata, err := server.findMap(mapname, players)
	if err != nil {
		writeJson(w, err.Error(), http.StatusBadRequest)
		return
	}
	// A random map is recorded with its seed, so that it can be played again
	if mapname == RandomMap {
		mapname = fmt.Sprintf("%s:%d", RandomMap, mapdata.Seed)
	}

	var names []string
	if namesStr := r.URL.Query().Get("names"); namesStr != "" {
		names = strings.Split(namesStr, ",")
		if len(names) != players || slices.Contains(names, "") || len(slices.Compact(slices.Sorted(slices.Values(names)))) != len(names) {
			writeJson(w, "Invalid names: there must be one unique name per player", http.StatusBadRequest)
			return
		}
	}

	server.mutex.Lock()
	if MaxGames > 0 && server.created >= MaxGames {
		server.mutex.Unlock()
		writeJson(w, "This server does not accept new games", http.StatusForbidden)
		return
	}
	server.created++
	id := GenerateUniqueID(server.Sessions)
	game := NewGameSession(id, players, mapname, mapdata)
	server.Sessions[id] = game
	var joinIDs map[string]string
	if names != nil {
		joinIDs = game.ReserveSeats(names)
		for _, joinID := range joinIDs {
			server.JoinIDs[joinID] = game
		}
	}
	server.mutex.Unlock()

	time.AfterFunc(GameStartTimeout, func() { server.removeIfNotStarted(id) })
	server.removeIfOver(id)

	log.Printf("Created game %s (%s, %d players)", id, mapname, players)

	response := map[string]any{
		"id":          game.ID,
		"numPlayers":  players,
		"map":         game.Map,
		"createdDate": game.CreatedDate,
		"adminToken":  game.AdminToken,
	}
	if joinIDs != nil {
		response["joinIds"] = joinIDs
	}
	writeJson(w, response, http.StatusOK)
}

// removeGame forgets a game, with the private IDs of its reserved seats.
// The server mutex must be held.
func (server *Server) removeGame(id string) {
	game := server.Sessions[id]
	if game == nil {
		return
	}
	for joinID := range game.JoinIDs {
		delete(server.JoinIDs, joinID)
	}
	delete(server.Sessions, id)
}

func (server *Server) removeIfNotStarted(id string) {
	server.mutex.Lock()
	defer server.mutex.Unlock()

	game := server.Sessions[id]
	if game != nil && !game.Started() {
		server.removeGame(id)
		game.CloseSockets()
		log.Printf("Removed game %s because of timeout", id)
	}
}

func (server *Server) removeIfOver(id string) {
	server.mutex.Lock()
	defer server.mutex.Unlock()

	game := server.Sessions[id]
	if game == nil {
		return
	}
	if game.IsOver() {
		server.removeGame(id)
		return
	}

	time.AfterFunc(GameStartTimeout, func() { server.removeIfOver(id) })
}

// getGameSync returns the game with the given public ID, or with the
// private ID of one of its reserved seats
func (server *Server) getGameSync(id string) *GameSession {
	server.mutex.Lock()
	defer server.mutex.Unlock()

	if game := server.Sessions[id]; game != nil {
		return game
	}
	return server.JoinIDs[id]
}

func (server *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	logRoute(r)

	server.mutex.Lock()
	defer server.mutex.Unlock()

	var statuses = []arena.SessionStatus{}
	for _, session := range server.Sessions {
		statuses = append(statuses, session.Status())
	}

	response := arena.StatusResponse{
		GitRevision: GitRevision(),
		Games:       statuses,
	}

	writeJson(w, response, http.StatusOK)
}

func (server *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	logRoute(r)

	id := r.URL.Query().Get("id")
	game := server.getGameSync(id)
	if game == nil {
		writeJson(w, "Invalid game id: "+id, http.StatusBadRequest)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		writeJson(w, "Invalid name", http.StatusBadRequest)
		return
	}

	var player *Player
	var err error
	if id == game.ID {
		player, err = game.AddPlayer(name)
	} else {
		player, err = game.AddReservedPlayer(id)
	}
	if err != nil {
		writeJson(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Player %s joined game %s (#%d, %s)", player.Name, game.ID, player.ID, player.Token)

	if game.Started() {
		log.Printf("Game %s has started", id)
	}

	writeJson(w, map[string]any{
		"id":    player.ID,
		"token": player.Token,
	}, http.StatusOK)
}

func (server *Server) handleGame(w http.ResponseWriter, r *http.Request) {
	logRoute(r)

	id := r.URL.Query().Get("id")
	game := server.getGameSync(id)
	if game == nil {
		writeJson(w, "Invalid game id: "+id, http.StatusBadRequest)
		return
	}

	if !game.Started() {
		writeJson(w, "Game has not started", http.StatusBadRequest)
		return
	}

	token := r.URL.Query().Get("token")
	if token == game.AdminToken {
		writeJson(w, game.FullState(), http.StatusOK)
		return
	}

	view := game.GetView(token)
	if view == nil {
		writeJson(w, "Invalid token", http.StatusForbidden)
		return
	}

	writeJson(w, view, http.StatusOK)
}

func (server *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	logRoute(r)

	id := r.URL.Query().Get("id")
	game := server.getGameSync(id)
	if game == nil {
		writeJson(w, "Invalid game id: "+id, http.StatusBadRequest)
		return
	}

	token := r.URL.Query().Get("token")
	player, ok := game.Player(token)
	if !ok {
		writeJson(w, "Invalid token", http.StatusForbidden)
		return
	}

	turn, err := strconv.ParseUint(r.URL.Query().Get("turn"), 10, 0)
	if err != nil {
		writeJson(w, "Invalid turn: "+r.URL.Query().Get("turn"), http.StatusBadRequest)
		return
	}

	var orders []*arena.Order
	err = json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodySize)).Decode(&orders)
	if err != nil {
		writeJson(w, "Invalid or malformed JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if slices.Contains(orders, nil) {
		writeJson(w, "Invalid order: null", http.StatusBadRequest)
		return
	}

	if err := game.SetOrders(player.ID, uint(turn), orders); err != nil {
		writeJson(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJson(w, "OK", http.StatusOK)
}

func (server *Server) handleForfeit(w http.ResponseWriter, r *http.Request) {
	logRoute(r)

	id := r.URL.Query().Get("id")
	game := server.getGameSync(id)
	if game == nil {
		writeJson(w, "Invalid game id: "+id, http.StatusBadRequest)
		return
	}

	if r.URL.Query().Get("token") != game.AdminToken {
		writeJson(w, "Invalid token", http.StatusForbidden)
		return
	}

	player, err := strconv.Atoi(r.URL.Query().Get("player"))
	if err != nil {
		writeJson(w, "Invalid player: "+r.URL.Query().Get("player"), http.StatusBadRequest)
		return
	}

	if err := game.Forfeit(player); err != nil {
		writeJson(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJson(w, "OK", http.StatusOK)
}

func (server *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	logRoute(r)

	id := r.URL.Query().Get("id")
	game := server.getGameSync(id)
	if game == nil {
		writeJson(w, "Invalid game id: "+id, http.StatusBadRequest)
		return
	}

	upgrader := websocket.Upgrader{}
	socket, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("Failed to upgrade connection: " + err.Error())
		return
	}

	game.RegisterWebSocket(socket)
}

var validID = regexp.MustCompile(`^[a-z0-9-]+$`)

// findReport looks for the history file of a past game, and returns its path,
// or an empty string. If several games had the same ID, the most recent wins.
func findReport(id string) string {
	if HistoryDir == "" || !validID.MatchString(id) {
		return ""
	}

	files, _ := filepath.Glob(filepath.Join(HistoryDir, "*-"+id+"-*.json"))
	if len(files) == 0 {
		return ""
	}

	// File names begin with the creation date
	slices.Sort(files)
	return files[len(files)-1]
}

func (server *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	logRoute(r)

	id := r.URL.Query().Get("id")

	// Games stay in memory for a while after they end
	if game := server.getGameSync(id); game != nil {
		report := game.Report()
		if report == nil {
			writeJson(w, "Game is not over: "+id, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := report.WriteJSON(w); err != nil {
			log.Printf("Could not send the report of game %s: %s", id, err)
		}
		return
	}

	path := findReport(id)
	if path == "" {
		writeJson(w, "Game not found: "+id, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	http.ServeFile(w, r, path)
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /newgame", server.handleNewGame)
	mux.HandleFunc("GET /status", server.handleStatus)
	mux.HandleFunc("GET /join", server.handleJoin)
	mux.HandleFunc("GET /game", server.handleGame)
	mux.HandleFunc("POST /orders", server.handleOrders)
	mux.HandleFunc("GET /ws", server.handleWebSocket)
	mux.HandleFunc("GET /report", server.handleReport)
	mux.HandleFunc("GET /forfeit", server.handleForfeit)

	if HistoryDir != "" {
		fs := http.FileServer(http.Dir(HistoryDir))
		mux.Handle("GET /history/", http.StripPrefix("/history/", fs))
	}

	return mux
}

func RunServer(port int) {

	server := Server{
		Maps:     loadMaps(),
		Sessions: make(map[string]*GameSession),
		JoinIDs:  make(map[string]*GameSession),
	}

	log.Printf("Listening on port %d", port)

	err := http.ListenAndServe(":"+strconv.Itoa(port), server.Handler())
	fmt.Println(err)
}
