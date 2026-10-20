package main

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	. "hive-arena/common"
)

const MapDir = "maps"
const GameStartTimeout = 5 * time.Minute

var HistoryDir = "history"

type Server struct {
	mutex sync.Mutex

	Maps     map[string]MapData
	Sessions map[string]*GameSession
}

func loadMaps() map[string]MapData {

	data := make(map[string]MapData)

	entries, err := os.ReadDir(MapDir)
	if err != nil {
		log.Fatalf("Could not find maps directory")
	}

	for _, entry := range entries {
		name := entry.Name()
		path := MapDir + "/" + name
		mapdata, err := LoadMap(path)
		if err != nil {
			log.Fatalf("Could not load map %s: %s", name, err)
		}

		name = strings.ReplaceAll(name, ".txt", "")
		data[name] = mapdata
	}

	log.Printf("Loaded maps: %s", strings.Join(slices.Collect(maps.Keys(data)), ", "))

	return data
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

	mapname := r.URL.Query().Get("map")
	mapdata, mapfound := server.Maps[mapname]
	if !mapfound {
		writeJson(w, "Map not found: "+mapname, http.StatusBadRequest)
		return
	}

	playerStr := r.URL.Query().Get("players")
	players, ok := strconv.Atoi(playerStr)
	if ok != nil || !IsValidNumPlayers(players) {
		writeJson(w, "Invalid number of players: "+playerStr, http.StatusBadRequest)
		return
	}

	server.mutex.Lock()
	id := GenerateUniqueID(server.Sessions)
	game := NewGameSession(id, players, mapname, mapdata)
	server.Sessions[id] = game
	server.mutex.Unlock()

	time.AfterFunc(GameStartTimeout, func() { server.removeIfNotStarted(id) })
	server.removeIfOver(id)

	log.Printf("Created game %s (%s, %d players)", id, mapname, players)

	writeJson(w, map[string]any{
		"id":          game.ID,
		"numPlayers":  players,
		"map":         game.Map,
		"createdDate": game.CreatedDate,
		"adminToken":  game.AdminToken,
	}, http.StatusOK)
}

func (server *Server) removeIfNotStarted(id string) {
	server.mutex.Lock()
	defer server.mutex.Unlock()

	game := server.Sessions[id]
	if game != nil && !game.Started() {
		delete(server.Sessions, id)
		game.CloseSockets()
		log.Printf("Removed game %s because of timeout", id)
	}
}

func (server *Server) removeIfOver(id string) {
	server.mutex.Lock()
	defer server.mutex.Unlock()

	game := server.Sessions[id]
	if game != nil && game.IsOver() {
		delete(server.Sessions, id)
		return
	}

	time.AfterFunc(GameStartTimeout, func() { server.removeIfOver(id) })
}

func (server *Server) getGameSync(id string) *GameSession {
	server.mutex.Lock()
	defer server.mutex.Unlock()

	return server.Sessions[id]
}

func (server *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	logRoute(r)

	server.mutex.Lock()
	defer server.mutex.Unlock()

	var statuses = []SessionStatus{}
	for _, session := range server.Sessions {
		statuses = append(statuses, session.Status())
	}

	response := StatusResponse{
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

	player := game.AddPlayer(name)
	if player == nil {
		writeJson(w, "Game is full", http.StatusBadRequest)
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

	var orders []*Order
	err = json.NewDecoder(r.Body).Decode(&orders)
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

func RunServer(port int) {

	server := Server{
		Maps:     loadMaps(),
		Sessions: make(map[string]*GameSession),
	}

	http.HandleFunc("GET /newgame", server.handleNewGame)
	http.HandleFunc("GET /status", server.handleStatus)
	http.HandleFunc("GET /join", server.handleJoin)
	http.HandleFunc("GET /game", server.handleGame)
	http.HandleFunc("POST /orders", server.handleOrders)
	http.HandleFunc("GET /ws", server.handleWebSocket)

	fs := http.FileServer(http.Dir("./" + HistoryDir + "/"))
	http.Handle("GET /history/", http.StripPrefix("/history/", fs))

	log.Printf("Listening on port %d", port)

	err := http.ListenAndServe(":"+strconv.Itoa(port), nil)
	fmt.Println(err)
}
