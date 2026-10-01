// Package hub tracks live robot and dashboard WebSocket connections.
package hub

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ErrOffline is returned when a command targets a robot that is not connected.
var ErrOffline = errors.New("robot offline")

// Conn is one WebSocket with a single writer goroutine.
type Conn struct {
	ws   *websocket.Conn
	send chan []byte
	once sync.Once
}

// NewConn starts the writer (with keep-alive pings) for a WebSocket.
func NewConn(ws *websocket.Conn) *Conn {
	c := &Conn{ws: ws, send: make(chan []byte, 256)}
	go c.writer()
	return c
}

func (c *Conn) writer() {
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.ws.WriteMessage(websocket.CloseMessage, nil)
				_ = c.ws.Close()
				return
			}
			_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if c.ws.WriteMessage(websocket.TextMessage, msg) != nil {
				_ = c.ws.Close()
				return
			}
		case <-ping.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if c.ws.WriteMessage(websocket.PingMessage, nil) != nil {
				_ = c.ws.Close()
				return
			}
		}
	}
}

// Send queues a message; slow consumers drop messages instead of blocking the hub.
func (c *Conn) Send(msg []byte) {
	defer func() { _ = recover() }() // send on a closed channel after Close
	select {
	case c.send <- msg:
	default:
	}
}

// Close stops the writer and closes the socket.
func (c *Conn) Close() { c.once.Do(func() { close(c.send) }) }

// Hub routes messages between robots and dashboards.
type Hub struct {
	mu         sync.RWMutex
	robots     map[string]*Conn
	dashboards map[*Conn]struct{}
}

// New returns an empty hub.
func New() *Hub {
	return &Hub{robots: map[string]*Conn{}, dashboards: map[*Conn]struct{}{}}
}

// AddRobot registers a robot connection, replacing an older one.
func (h *Hub) AddRobot(id string, c *Conn) {
	h.mu.Lock()
	if old, ok := h.robots[id]; ok {
		old.Close()
	}
	h.robots[id] = c
	h.mu.Unlock()
}

// RemoveRobot unregisters a robot connection if it is still the current one.
func (h *Hub) RemoveRobot(id string, c *Conn) {
	h.mu.Lock()
	if h.robots[id] == c {
		delete(h.robots, id)
	}
	h.mu.Unlock()
	c.Close()
}

// Online reports whether a robot is connected.
func (h *Hub) Online(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.robots[id]
	return ok
}

// SendRobot sends a JSON message to a robot.
func (h *Hub) SendRobot(id string, msg any) error {
	h.mu.RLock()
	c, ok := h.robots[id]
	h.mu.RUnlock()
	if !ok {
		return ErrOffline
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.Send(b)
	return nil
}

// AddDashboard registers a dashboard connection.
func (h *Hub) AddDashboard(c *Conn) {
	h.mu.Lock()
	h.dashboards[c] = struct{}{}
	h.mu.Unlock()
}

// RemoveDashboard unregisters a dashboard connection.
func (h *Hub) RemoveDashboard(c *Conn) {
	h.mu.Lock()
	delete(h.dashboards, c)
	h.mu.Unlock()
	c.Close()
}

// Broadcast sends a JSON message to every dashboard.
func (h *Hub) Broadcast(msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.dashboards {
		c.Send(b)
	}
}
