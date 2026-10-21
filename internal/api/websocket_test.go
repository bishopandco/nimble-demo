package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
		if message := receiveSocket(t, connection); message.Type != "pong" || message.ID != 0 {
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
