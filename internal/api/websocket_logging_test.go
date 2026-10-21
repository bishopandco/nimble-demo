package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-api/internal/api"
)

// The HTTP handler and socket writer can log concurrently.
type socketLogBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *socketLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *socketLogBuffer) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func TestWebSocketLogging(t *testing.T) {
	t.Parallel()
	var output socketLogBuffer
	handler, err := api.New(context.Background(), testStore(t), slog.New(slog.NewJSONHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Close()
	conn := connectSocket(t, server, nil)
	const ping = "{\n  \"type\": \"ping\"\n}"
	for _, payload := range []string{ping, "not json"} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		receiveSocket(t, conn)
	}
	// Rejected connections must also use the API's supplied logger.
	rejected, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", http.Header{"Origin": []string{"https://example.com"}})
	if rejected != nil {
		rejected.Close()
	}
	if response != nil {
		response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected origin rejection: %v", err)
	}
	handler.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Fatalf("expected shutdown close: %v", err)
	}

	decoder := json.NewDecoder(strings.NewReader(output.snapshot()))
	records := make(map[string][]map[string]any)
	for {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
		if record["component"] != "websocket" || record["remote_addr"] == "" || record["remote_addr"] == nil {
			t.Fatalf("missing socket context: %v", record)
		}
		message, _ := record["msg"].(string)
		records[message] = append(records[message], record)
	}
	for message, count := range map[string]int{
		"socket connected":           1,
		"socket message received":    2,
		"socket message rejected":    1,
		"socket connection rejected": 1,
		"socket disconnected":        1,
	} {
		if len(records[message]) != count {
			t.Fatalf("%s: expected %d records, got %d\n%s", message, count, len(records[message]), output.snapshot())
		}
	}
	connectionID := records["socket connected"][0]["connection_id"]
	if connectionID == nil || connectionID == float64(0) {
		t.Fatal("missing connection ID")
	}
	for _, message := range []string{"socket connected", "socket message received", "socket message rejected", "socket disconnected"} {
		for _, record := range records[message] {
			if record["connection_id"] != connectionID {
				t.Fatalf("connection ID changed: %v", record)
			}
		}
	}
	for i, payload := range []string{ping, "not json"} {
		record := records["socket message received"][i]
		if record["payload"] != payload || record["bytes"] != float64(len(payload)) || record["frame_type"] != "text" || record["level"] != "INFO" {
			t.Fatalf("incorrect incoming-message log: %v", record)
		}
	}
	for _, message := range []string{"socket message rejected", "socket connection rejected"} {
		if records[message][0]["level"] != "WARN" {
			t.Fatalf("rejection was not logged as warning: %v", records[message][0])
		}
	}
	if records["socket connection rejected"][0]["connection_id"] == connectionID {
		t.Fatal("separate connections share an ID")
	}
	disconnected := records["socket disconnected"][0]
	if disconnected["reason"] != "server shutting down" || disconnected["messages_received"] != float64(2) || disconnected["duration"] == nil {
		t.Fatalf("incorrect disconnect log: %v", disconnected)
	}
}
