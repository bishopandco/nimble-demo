package api_test

import (
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"go-api/internal/api"
)

func connectChat(t *testing.T, server *httptest.Server) (*websocket.Conn, api.SocketMessage) {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	ready := receiveSocket(t, conn)
	if ready.Type != "ready" || ready.ClientID == 0 {
		t.Fatalf("missing chat identity: %+v", ready)
	}
	return conn, ready
}

func sendChat(t *testing.T, conn *websocket.Conn, name, message string) {
	t.Helper()
	if err := conn.WriteJSON(map[string]string{"type": "chat", "name": name, "message": message}); err != nil {
		t.Fatal(err)
	}
}

func TestChatSharedWithEveryConnection(t *testing.T) {
	t.Parallel()
	_, server, _ := socketServer(t)
	alice, aliceReady := connectChat(t, server)
	bob, bobReady := connectChat(t, server)
	if aliceReady.ClientID == bobReady.ClientID {
		t.Fatal("clients share an identity")
	}
	sendChat(t, alice, " Alice ", " Hello everyone! ")
	first := receiveSocket(t, alice)
	if first.Type != "chat" || first.ID != 1 || first.ClientID != aliceReady.ClientID || first.Name != "Alice" || first.Message != "Hello everyone!" {
		t.Fatalf("invalid shared message: %+v", first)
	}
	if peer := receiveSocket(t, bob); !reflect.DeepEqual(peer, first) {
		t.Fatalf("peer received a different message: %+v", peer)
	}
	sendChat(t, bob, "", "Hi Alice")
	second := receiveSocket(t, bob)
	if second.ID != 2 || second.ClientID != bobReady.ClientID || second.Name != fmt.Sprintf("Guest %d", bobReady.ClientID) {
		t.Fatalf("invalid guest message: %+v", second)
	}
	if peer := receiveSocket(t, alice); !reflect.DeepEqual(peer, second) {
		t.Fatal("sender and peer messages differ")
	}
	late, ready := connectChat(t, server)
	if !reflect.DeepEqual(ready.History, []api.SocketMessage{first, second}) {
		t.Fatalf("new connection missed shared history: %+v", ready.History)
	}
	sendChat(t, late, "Charlie", "I can see both messages")
	var broadcast api.SocketMessage
	for i, conn := range []*websocket.Conn{alice, bob, late} {
		message := receiveSocket(t, conn)
		if i == 0 {
			broadcast = message
		} else if !reflect.DeepEqual(message, broadcast) {
			t.Fatal("room members received different messages")
		}
	}
	if broadcast.ID != 3 || broadcast.ClientID != ready.ClientID {
		t.Fatal("message identity was not assigned by the server")
	}
}

func TestChatValidationIsPrivateToSender(t *testing.T) {
	t.Parallel()
	_, server, _ := socketServer(t)
	sender, _ := connectChat(t, server)
	observer, _ := connectChat(t, server)
	for _, payload := range []string{
		`{"type":"chat","message":""}`,
		`{"type":"chat","message":"   "}`,
		fmt.Sprintf(`{"type":"chat","message":%q}`, strings.Repeat("a", 501)),
		fmt.Sprintf(`{"type":"chat","name":%q,"message":"Hi"}`, strings.Repeat("a", 41)),
		`{"type":"chat","name":"Alice","message":"Hi","client_id":999}`,
		`{"type":"chat","name":"Alice","message":"Hi","id":999}`,
		`{"type":"chat","message":"Hi","timestamp":"fake"}`,
		`{"type":"ping","message":"Hi"}`,
	} {
		if err := sender.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		if message := receiveSocket(t, sender); message.Type != "error" || message.Message == "" {
			t.Fatalf("invalid chat accepted: %s, %+v", payload, message)
		}
	}
	if err := observer.WriteJSON(map[string]string{"type": "ping"}); err != nil {
		t.Fatal(err)
	}
	if message := receiveSocket(t, observer); message.Type != "pong" {
		t.Fatalf("invalid messages were broadcast: %+v", message)
	}
	_, ready := connectChat(t, server)
	if len(ready.History) != 0 {
		t.Fatal("invalid messages entered history")
	}
	// Character limits count Unicode code points, not bytes.
	sendChat(t, sender, strings.Repeat("界", 40), strings.Repeat("🌍", 500))
	if message := receiveSocket(t, sender); message.Type != "chat" {
		t.Fatalf("valid Unicode chat rejected: %+v", message)
	}
	if message := receiveSocket(t, observer); message.Type != "chat" {
		t.Fatal("Unicode message was not shared")
	}
}

func TestChatHistoryIsBounded(t *testing.T) {
	t.Parallel()
	_, server, _ := socketServer(t)
	sender, _ := connectChat(t, server)
	for i := 1; i <= 105; i++ {
		sendChat(t, sender, "Alice", fmt.Sprintf("Message %d", i))
		if message := receiveSocket(t, sender); message.ID != int64(i) {
			t.Fatalf("unexpected message ID: %+v", message)
		}
	}
	_, ready := connectChat(t, server)
	if len(ready.History) != 100 || ready.History[0].ID != 6 || ready.History[99].ID != 105 {
		t.Fatal("history did not retain the latest 100 messages")
	}
}

func TestConcurrentChatHasSameOrderForEveryone(t *testing.T) {
	t.Parallel()
	_, server, _ := socketServer(t)
	first, _ := connectChat(t, server)
	second, _ := connectChat(t, server)
	done := make(chan error, 2)
	for i, conn := range []*websocket.Conn{first, second} {
		go func() {
			for n := range 10 {
				if err := conn.WriteJSON(map[string]string{"type": "chat", "name": fmt.Sprintf("User %d", i), "message": fmt.Sprintf("Message %d", n)}); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	for i := range 20 {
		a, b := receiveSocket(t, first), receiveSocket(t, second)
		if !reflect.DeepEqual(a, b) || a.ID != int64(i+1) {
			t.Fatalf("clients disagree on message order: %+v, %+v", a, b)
		}
	}
}
