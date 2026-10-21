package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go-api/internal/store"
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
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub, err := newSocketHub(context.Background(), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer hub.close()
	newClient := func() *socketClient {
		peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { peer.Close() })
		conn := <-upgraded
		identity, err := db.CreateConnection(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		client := &socketClient{id: identity.ID, conn: conn, send: make(chan SocketMessage, 1), done: make(chan struct{})}
		hub.clients[client] = struct{}{}
		return client
	}
	slow, healthy := newClient(), newClient()
	hub.chat(context.Background(), healthy, "Ada", "First message")
	<-healthy.send
	done := make(chan struct{})
	go func() {
		hub.chat(context.Background(), healthy, "Ada", "Second message")
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
	if message := <-healthy.send; message.Type != "chat" || message.ID != 2 || message.Message != "Second message" {
		t.Fatalf("healthy client missed event: %+v", message)
	}
	hub.mu.Lock()
	_, remains := hub.clients[slow]
	hub.mu.Unlock()
	if remains {
		t.Fatal("slow client remains registered")
	}
}
