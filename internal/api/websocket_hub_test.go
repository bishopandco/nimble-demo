package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSocketHubDropsFullQueue(t *testing.T) {
	// Use real connections without write pumps so queue pressure is deterministic.
	upgraded := make(chan *websocket.Conn, 2)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		upgraded <- conn
	}))
	defer server.Close()
	hub := newSocketHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer hub.close()
	newClient := func() *socketClient {
		peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { peer.Close() })
		conn := <-upgraded
		client := &socketClient{conn: conn, send: make(chan SocketMessage, 1), done: make(chan struct{})}
		hub.clients[client] = struct{}{}
		return client
	}
	slow, healthy := newClient(), newClient()
	hub.publish("created", "tags", 1)
	<-healthy.send
	done := make(chan struct{})
	go func() {
		hub.publish("updated", "tags", 1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("slow client blocked broadcast")
	}
	select {
	case <-slow.done:
		if slow.reason != "outgoing queue full" {
			t.Fatalf("incorrect slow-client close reason: %s", slow.reason)
		}
	default:
		t.Fatal("slow client was not disconnected")
	}
	if message := <-healthy.send; message.Type != "updated" {
		t.Fatalf("healthy client missed event: %+v", message)
	}
	hub.mu.Lock()
	_, remains := hub.clients[slow]
	hub.mu.Unlock()
	if remains {
		t.Fatal("slow client remains registered")
	}
}
