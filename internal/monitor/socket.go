package monitor

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go-api/internal/model"
)

type subscriber struct {
	conn *websocket.Conn
	send chan model.MonitorSnapshot
	done chan struct{}
	once sync.Once
}

func (c *subscriber) close() { c.once.Do(func() { close(c.done); _ = c.conn.Close() }) }
func (c *subscriber) shutdown(deadline time.Time) {
	_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"), deadline)
	c.close()
}

func socketError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		socketError(w, 405, "WebSocket connections require GET")
		return
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		socketError(w, 503, "monitor is shutting down")
		return
	}
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, Error: func(w http.ResponseWriter, r *http.Request, status int, err error) {
		socketError(w, status, http.StatusText(status))
	}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &subscriber{conn: conn, send: make(chan model.MonitorSnapshot, 8), done: make(chan struct{})}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		client.close()
		return
	}
	s.clients[client] = struct{}{}
	client.send <- s.latest
	s.connections.Add(1)
	s.mu.Unlock()
	defer func() { client.close(); s.mu.Lock(); delete(s.clients, client); s.mu.Unlock(); s.connections.Done() }()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer client.close()
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-client.done:
				return
			case current := <-client.send:
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteJSON(current); err != nil {
					return
				}
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			}
		}
	}()
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	_, _, _ = conn.ReadMessage() // Application frames are not accepted on this read-only feed.
	client.close()
	<-writerDone
}
