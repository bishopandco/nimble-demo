package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"go-api/internal/api"
	"go-api/internal/store"
)

func persistentServer(t *testing.T, path string) (*store.Store, *httptest.Server, *api.Server) {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.New(context.Background(), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(func() { handler.Close(); server.Close(); db.Close() })
	return db, server, handler
}

func TestChatSurvivesServerAndDatabaseRestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "chat.db")
	db, server, handler := persistentServer(t, path)
	first, identity := connectChat(t, server)
	sendChat(t, first, " Ada ", " Saved message\nwith two lines. ")
	message := receiveSocket(t, first)
	stored, err := db.RecentMessages(context.Background(), 100)
	if err != nil || len(stored) != 1 || !reflect.DeepEqual(stored[0].SocketMessage(), message) {
		t.Fatalf("broadcast preceded persistence: %+v, %v", stored, err)
	}
	_, unused := connectChat(t, server)
	handler.Close()
	server.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, restarted, _ := persistentServer(t, path)
	second, ready := connectChat(t, restarted)
	if !reflect.DeepEqual(ready.History, []api.SocketMessage{message}) {
		t.Fatalf("history lost after restart: %+v", ready)
	}
	if ready.ClientID <= unused.ClientID || ready.ClientID == identity.ClientID {
		t.Fatal("historical message could be attributed to a new connection")
	}
	sendChat(t, second, "Grace", "New message")
	next := receiveSocket(t, second)
	if next.ID != message.ID+1 || next.ClientID != ready.ClientID {
		t.Fatalf("invalid post-restart identity: %+v", next)
	}
}

func TestChatPersistenceFailureIsPrivate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "chat.db")
	db, server, handler := persistentServer(t, path)
	sender, _ := connectChat(t, server)
	observer, _ := connectChat(t, server)
	sendChat(t, sender, "Ada", "Saved")
	saved := receiveSocket(t, sender)
	receiveSocket(t, observer)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	sendChat(t, sender, "Ada", "Must not appear")
	if response := receiveSocket(t, sender); response.Type != "error" || response.Message != "Message was not saved. Please try again." {
		t.Fatalf("failed save was acknowledged as chat: %+v", response)
	}
	if err := observer.WriteJSON(map[string]string{"type": "ping"}); err != nil {
		t.Fatal(err)
	}
	if response := receiveSocket(t, observer); response.Type != "pong" {
		t.Fatalf("failed save was broadcast: %+v", response)
	}
	c := &client{t: t, h: handler}
	c.request("GET", "/health", "", http.StatusServiceUnavailable)
	handler.Close()
	server.Close()
	_, restarted, _ := persistentServer(t, path)
	_, ready := connectChat(t, restarted)
	if !reflect.DeepEqual(ready.History, []api.SocketMessage{saved}) {
		t.Fatalf("failed save entered durable history: %+v", ready.History)
	}
}

func TestStartupFailsWithoutHistoryStore(t *testing.T) {
	db := testStore(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if handler, err := api.New(context.Background(), db, nil); err == nil {
		handler.Close()
		t.Fatal("started without loading persisted history")
	}
}
