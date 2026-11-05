package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// writeTimeout bounds how long one slow browser can hold up a broadcast.
const writeTimeout = 5 * time.Second

// hub tracks the open WebSocket connections so a message can go to all of
// them, with who each one signed in as.
type hub struct {
	mu    sync.Mutex
	conns map[*websocket.Conn]string
}

func newHub() *hub {
	return &hub{conns: make(map[*websocket.Conn]string)}
}

func (h *hub) add(c *websocket.Conn, who string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[c] = who
}

func (h *hub) remove(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, c)
}

func (h *hub) snapshot() []*websocket.Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	conns := make([]*websocket.Conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	return conns
}

// broadcast sends m to every open connection. A connection that fails to
// take the write is closed, and its handler removes it.
func (h *hub) broadcast(m Message) {
	data, err := json.Marshal(m)
	if err != nil {
		slog.Error("web: encode message", "type", m.Type, "err", err)
		return
	}
	for _, c := range h.snapshot() {
		if err := writeWithTimeout(c, data); err != nil {
			_ = c.CloseNow()
		}
	}
}

// drop closes every connection whose sign-in keep refuses. It stops
// sending to them at once, and closes them in the background, since a
// close waits for the browser to answer.
func (h *hub) drop(keep func(who string) bool) {
	h.mu.Lock()
	var gone []*websocket.Conn
	for c, who := range h.conns {
		if !keep(who) {
			gone = append(gone, c)
			delete(h.conns, c)
		}
	}
	h.mu.Unlock()
	for _, c := range gone {
		go func() { _ = c.Close(StatusSignIn, "sign in again") }()
	}
}

// closeAll tells every browser the citadel is going away.
func (h *hub) closeAll() {
	for _, c := range h.snapshot() {
		_ = c.Close(websocket.StatusGoingAway, "citadel stopping")
	}
}

func writeWithTimeout(c *websocket.Conn, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, data)
}
