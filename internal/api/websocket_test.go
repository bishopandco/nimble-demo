package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-api/internal/api"
)

func socketServer(t *testing.T) (*client, *httptest.Server, *api.Server) {
	t.Helper()
	c := setup(t)
	handler := c.h.(*api.Server)
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		handler.Close()
		server.Close()
	})
	return c, server, handler
}

func connectSocket(t *testing.T, server *httptest.Server, headers http.Header) *websocket.Conn {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", headers)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if message := receiveSocket(t, conn); message.Type != "ready" {
		t.Fatalf("expected ready message, got %+v", message)
	}
	return conn
}

func receiveSocket(t *testing.T, conn *websocket.Conn) api.SocketMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var message api.SocketMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatal(err)
	}
	if message.Timestamp.IsZero() || message.Timestamp.Location() != time.UTC {
		t.Fatalf("missing UTC timestamp: %+v", message)
	}
	return message
}

func TestWebSocketBroadcastsAllResources(t *testing.T) {
	t.Parallel()
	c, server, _ := socketServer(t)
	connections := []*websocket.Conn{connectSocket(t, server, nil), connectSocket(t, server, nil)}
	operations := []struct {
		method, path, body, action, resource string
		status                               int
	}{
		{"POST", "/users", `{"name":"Ada","email":"ada@example.com"}`, "created", "users", 201},
		{"POST", "/tags", `{"name":"Go"}`, "created", "tags", 201},
		{"POST", "/blogs", `{"title":"First","body":"Hello","author_id":1,"tag_ids":[1]}`, "created", "blogs", 201},
		{"POST", "/comments", `{"blog_id":1,"author_id":1,"body":"Nice post"}`, "created", "comments", 201},
		{"PUT", "/users/1", `{"name":"Ada Updated","email":"ada@example.com"}`, "updated", "users", 200},
		{"PUT", "/tags/1", `{"name":"Golang"}`, "updated", "tags", 200},
		{"PUT", "/blogs/1", `{"title":"Updated","body":"Hello","author_id":1,"tag_ids":[1]}`, "updated", "blogs", 200},
		{"PUT", "/comments/1", `{"blog_id":1,"author_id":1,"body":"Updated"}`, "updated", "comments", 200},
		{"DELETE", "/comments/1", "", "deleted", "comments", 204},
		{"DELETE", "/blogs/1", "", "deleted", "blogs", 204},
		{"DELETE", "/tags/1", "", "deleted", "tags", 204},
		{"DELETE", "/users/1", "", "deleted", "users", 204},
	}
	for _, op := range operations {
		t.Run(op.method+" "+op.path, func(t *testing.T) {
			c.request(op.method, op.path, op.body, op.status)
			var first api.SocketMessage
			for index, conn := range connections {
				message := receiveSocket(t, conn)
				if message.Type != op.action || message.Resource != op.resource || message.ID != 1 || message.Message != "" {
					t.Fatalf("unexpected change event: %+v", message)
				}
				if index == 0 {
					first = message
				} else if !reflect.DeepEqual(message, first) {
					t.Fatal("clients received different events")
				}
			}
		})
	}
}

func TestWebSocketDoesNotPublishFailedWritesOrReads(t *testing.T) {
	t.Parallel()
	c, server, _ := socketServer(t)
	c.seed()
	conn := connectSocket(t, server, nil)
	c.request("POST", "/users", `{"name":"Other","email":"ada@example.com"}`, 409)
	c.request("POST", "/tags", `{"name":"Go"}`, 409)
	c.request("POST", "/tags", `{"name":""}`, 400)
	c.request("POST", "/blogs", `{"title":"Invalid","body":"Text","author_id":1,"tag_ids":[1,9999]}`, 409)
	c.request("PUT", "/blogs/1", `{"title":"Invalid","body":"Text","author_id":1,"tag_ids":[1,9999]}`, 409)
	c.request("DELETE", "/users/1", "", 409)
	c.request("DELETE", "/tags/9999", "", 404)
	c.request("GET", "/blogs/1", "", 200)
	c.request("GET", "/tags", "", 200)
	// A pong acts as a queue barrier: any mistakenly published event precedes it.
	if err := conn.WriteJSON(map[string]string{"type": "ping"}); err != nil {
		t.Fatal(err)
	}
	if message := receiveSocket(t, conn); message.Type != "pong" {
		t.Fatalf("unexpected event after failed write/read: %+v", message)
	}
}

func TestWebSocketJSONMessages(t *testing.T) {
	t.Parallel()
	_, server, _ := socketServer(t)
	conn := connectSocket(t, server, nil)
	other := connectSocket(t, server, nil)
	for _, payload := range []string{`{`, `null`, `[]`, `{}`, `{"type":"unknown"}`, `{"type":"ping","extra":true}`, `{"type":"ping"} {}`, `{"type":123}`} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		if message := receiveSocket(t, conn); message.Type != "error" || message.Message == "" {
			t.Fatalf("%s: got %+v", payload, message)
		}
	}
	for _, connection := range []*websocket.Conn{conn, other} {
		if err := connection.WriteJSON(map[string]string{"type": "ping"}); err != nil {
			t.Fatal(err)
		}
		if message := receiveSocket(t, connection); message.Type != "pong" || message.Resource != "" || message.ID != 0 {
			t.Fatalf("unexpected pong: %+v", message)
		}
	}
}

func TestWebSocketMessageLimits(t *testing.T) {
	t.Parallel()
	_, server, _ := socketServer(t)
	for _, tc := range []struct {
		name    string
		kind    int
		payload []byte
		code    int
	}{
		{"binary", websocket.BinaryMessage, []byte("ping"), websocket.CloseUnsupportedData},
		{"invalid UTF-8", websocket.TextMessage, []byte{0xff}, websocket.CloseInvalidFramePayloadData},
		{"oversized", websocket.TextMessage, bytes.Repeat([]byte("x"), 4097), websocket.CloseMessageTooBig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := connectSocket(t, server, nil)
			if err := conn.WriteMessage(tc.kind, tc.payload); err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _, err := conn.ReadMessage()
			if !websocket.IsCloseError(err, tc.code) {
				t.Fatalf("expected close %d, got %v", tc.code, err)
			}
		})
	}
}

func TestWebSocketOriginsAndUpgrade(t *testing.T) {
	t.Parallel()
	c, server, _ := socketServer(t)
	connectSocket(t, server, http.Header{"Origin": []string{server.URL}})
	for _, origin := range []string{"https://example.com", "null", "://invalid"} {
		conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", http.Header{"Origin": []string{origin}})
		if conn != nil {
			conn.Close()
		}
		if err == nil || response == nil {
			t.Fatalf("origin %q: expected rejection, got %v", origin, err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != 403 || !json.Valid(body) {
			t.Fatalf("origin %q: status=%d body=%s error=%v", origin, response.StatusCode, body, readErr)
		}
	}
	c.request("GET", "/ws", "", 400)
	c.request("POST", "/ws", `{"type":"ping"}`, 405)
	c.request("HEAD", "/ws", "", 405)
}

func TestWebSocketShutdown(t *testing.T) {
	t.Parallel()
	c, server, handler := socketServer(t)
	connections := []*websocket.Conn{connectSocket(t, server, nil), connectSocket(t, server, nil)}
	handler.Close()
	for _, conn := range connections {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, err := conn.ReadMessage()
		if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
			t.Fatalf("expected shutdown close, got %v", err)
		}
	}
	handler.Close()
	c.request("GET", "/ws", "", 503)
}

func TestWebSocketConcurrentWrites(t *testing.T) {
	t.Parallel()
	_, server, _ := socketServer(t)
	conn := connectSocket(t, server, nil)
	const count = 12
	results := make(chan error, count)
	for i := range count {
		go func() {
			body := fmt.Sprintf(`{"name":"tag-%d"}`, i)
			response, err := server.Client().Post(server.URL+"/tags", "application/json", strings.NewReader(body))
			if err == nil {
				response.Body.Close()
				if response.StatusCode != 201 {
					err = fmt.Errorf("write returned %d", response.StatusCode)
				}
			}
			results <- err
		}()
	}
	for range count {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[int64]bool)
	for range count {
		message := receiveSocket(t, conn)
		if message.Type != "created" || message.Resource != "tags" || message.ID <= 0 || seen[message.ID] {
			t.Fatalf("unexpected concurrent event: %+v", message)
		}
		seen[message.ID] = true
	}
}
