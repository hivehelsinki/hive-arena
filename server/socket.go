package main

import (
	"time"

	"github.com/gorilla/websocket"
)

const SocketWriteTimeout = 5 * time.Second
const SocketQueueSize = 16

// A websocket listening to a game. Messages are written by a dedicated
// goroutine, so that a slow client cannot block the game.
type Socket struct {
	conn *websocket.Conn
	send chan []byte
}

func NewSocket(conn *websocket.Conn, onClosed func(*Socket)) *Socket {
	socket := &Socket{conn, make(chan []byte, SocketQueueSize)}

	go socket.writeLoop()
	go socket.readLoop(onClosed)

	return socket
}

// Writes queued messages until the queue is closed, then closes the connection
func (socket *Socket) writeLoop() {
	defer socket.conn.Close()

	for message := range socket.send {
		socket.conn.SetWriteDeadline(time.Now().Add(SocketWriteTimeout))
		if err := socket.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			return
		}
	}

	socket.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}

// Clients are not expected to send anything, but reading is needed to handle
// control messages and to notice when the client goes away
func (socket *Socket) readLoop(onClosed func(*Socket)) {
	for {
		if _, _, err := socket.conn.NextReader(); err != nil {
			onClosed(socket)
			return
		}
	}
}

// Queues a message without blocking. Returns false if the client is too slow
// to keep up.
func (socket *Socket) Send(message []byte) bool {
	select {
	case socket.send <- message:
		return true
	default:
		return false
	}
}

// Closes the connection once queued messages are sent. Must be called once.
func (socket *Socket) Close() {
	close(socket.send)
}
