package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	. "hive-arena/common"
)

// dialSession opens a websocket registered to the given session
func dialSession(t *testing.T, session *GameSession) *websocket.Conn {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		session.RegisterWebSocket(conn)
	}))
	t.Cleanup(server.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func readMessage(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()

	conn.SetReadDeadline(time.Now().Add(time.Second))
	var message map[string]any
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatal(err)
	}
	return message
}

func socketCount(session *GameSession) int {
	session.mutex.Lock()
	defer session.mutex.Unlock()

	return len(session.Sockets)
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	for range 100 {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestSocketReceivesTurns(t *testing.T) {
	session, players := newTestSession(t, 2)
	conn := dialSession(t, session)

	if turn := readMessage(t, conn)["turn"]; turn != 0.0 {
		t.Errorf("expected turn 0, got %v", turn)
	}

	for _, player := range players {
		session.SetOrders(player.ID, 0, []*Order{})
	}

	if turn := readMessage(t, conn)["turn"]; turn != 1.0 {
		t.Errorf("expected turn 1, got %v", turn)
	}
}

func TestClosedSocketsAreRemoved(t *testing.T) {
	session, _ := newTestSession(t, 2)
	conn := dialSession(t, session)

	waitFor(t, func() bool { return socketCount(session) == 1 })

	conn.Close()
	waitFor(t, func() bool { return socketCount(session) == 0 })
}

func TestCloseSockets(t *testing.T) {
	DevMode = true

	mapdata, err := LoadMap("../maps/tiny.txt")
	if err != nil {
		t.Fatal(err)
	}
	session := NewGameSession("test", 2, "tiny", mapdata)
	conn := dialSession(t, session)

	waitFor(t, func() bool { return socketCount(session) == 1 })
	session.CloseSockets()

	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Errorf("expected the socket to be closed, got %v", err)
	}
}
