package api_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go-api/internal/api"
	"go-api/internal/bot"
	"go-api/internal/store"
)

type botFunc func(context.Context, bot.Request) (bot.Response, error)

func (f botFunc) Generate(ctx context.Context, r bot.Request) (bot.Response, error) { return f(ctx, r) }

func botServer(t *testing.T, db *store.Store, generator bot.Generator, timeout time.Duration) (*httptest.Server, *api.Server) {
	t.Helper()
	handler, err := api.New(context.Background(), db, slog.New(slog.NewTextHandler(io.Discard, nil)), api.WithBot(generator, timeout))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(func() { handler.Close(); server.Close() })
	return server, handler
}

func receiveBotStatus(t *testing.T, conn *websocket.Conn, state string) api.SocketMessage {
	t.Helper()
	message := receiveSocket(t, conn)
	if message.Type != "bot_status" || message.State != state {
		t.Fatalf("expected bot %s, got %+v", state, message)
	}
	return message
}

func TestBotRepliesAreSharedPersistedAndDoNotBlockChat(t *testing.T) {
	t.Parallel()
	db := testStore(t)
	requests := make(chan bot.Request, 1)
	release := make(chan struct{})
	generator := botFunc(func(ctx context.Context, request bot.Request) (bot.Response, error) {
		requests <- request
		select {
		case <-release:
			return bot.Response{Provider: "codex", Output: strings.Repeat("界", 650)}, nil
		case <-ctx.Done():
			return bot.Response{}, ctx.Err()
		}
	})
	server, handler := botServer(t, db, generator, time.Second)
	first, firstReady := connectChat(t, server)
	second, _ := connectChat(t, server)
	sendChat(t, first, "Ada", "/bot codex Explain sockets")
	prompt := receiveSocket(t, first)
	if prompt.Type != "chat" || prompt.Message != "/bot codex Explain sockets" {
		t.Fatalf("missing prompt: %+v", prompt)
	}
	if peer := receiveSocket(t, second); !reflect.DeepEqual(peer, prompt) {
		t.Fatal("prompt not shared")
	}
	for _, conn := range []*websocket.Conn{first, second} {
		receiveBotStatus(t, conn, "queued")
		receiveBotStatus(t, conn, "running")
	}
	select {
	case request := <-requests:
		if request.Provider != "codex" || request.Prompt != "Explain sockets" {
			t.Fatalf("wrong request: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not receive prompt")
	}
	if err := first.WriteJSON(map[string]string{"type": "ping"}); err != nil {
		t.Fatal(err)
	}
	if message := receiveSocket(t, first); message.Type != "pong" {
		t.Fatal("provider blocked socket reads")
	}
	sendChat(t, second, "Grace", "Still chatting")
	for _, conn := range []*websocket.Conn{first, second} {
		if message := receiveSocket(t, conn); message.Message != "Still chatting" {
			t.Fatalf("normal chat blocked: %+v", message)
		}
	}
	close(release)
	var replies []api.SocketMessage
	for range 2 {
		message := receiveSocket(t, first)
		if message.Type != "chat" || message.Provider != "codex" || message.ReplyTo != prompt.ID || message.Name != "Codex" || message.ClientID == firstReady.ClientID {
			t.Fatalf("invalid bot reply: %+v", message)
		}
		if peer := receiveSocket(t, second); !reflect.DeepEqual(peer, message) {
			t.Fatal("reply not shared")
		}
		replies = append(replies, message)
	}
	for _, conn := range []*websocket.Conn{first, second} {
		receiveBotStatus(t, conn, "done")
	}
	if replies[0].Message+replies[1].Message != strings.Repeat("界", 650) {
		t.Fatal("long output changed")
	}
	saved, err := db.RecentMessages(context.Background(), 100)
	if err != nil || len(saved) != 4 || !reflect.DeepEqual(saved[2].SocketMessage(), replies[0]) {
		t.Fatalf("bot reply not persisted: %+v, %v", saved, err)
	}
	handler.Close()
	server.Close()
	restarted, _ := botServer(t, db, generator, time.Second)
	_, ready := connectChat(t, restarted)
	if len(ready.History) != 4 || !reflect.DeepEqual(ready.History[3], replies[1]) {
		t.Fatal("bot metadata did not survive reload")
	}
}

func TestBotErrorsAndTimeoutArePrivate(t *testing.T) {
	for _, failure := range []string{"error", "timeout", "save"} {
		t.Run(failure, func(t *testing.T) {
			db := testStore(t)
			generator := botFunc(func(ctx context.Context, request bot.Request) (bot.Response, error) {
				if failure == "timeout" {
					<-ctx.Done()
					return bot.Response{}, ctx.Err()
				}
				if failure == "save" {
					_ = db.Close()
					return bot.Response{Provider: "claude", Output: "Should not appear"}, nil
				}
				return bot.Response{}, errors.New("provider unavailable")
			})
			server, _ := botServer(t, db, generator, 20*time.Millisecond)
			sender, _ := connectChat(t, server)
			observer, _ := connectChat(t, server)
			sendChat(t, sender, "Ada", "/bot claude Hello")
			for _, conn := range []*websocket.Conn{sender, observer} {
				receiveSocket(t, conn)
				receiveBotStatus(t, conn, "queued")
				receiveBotStatus(t, conn, "running")
			}
			if message := receiveSocket(t, sender); message.Type != "error" || message.Message == "" {
				t.Fatalf("missing private failure: %+v", message)
			}
			for _, conn := range []*websocket.Conn{sender, observer} {
				receiveBotStatus(t, conn, "failed")
			}
			if err := observer.WriteJSON(map[string]string{"type": "ping"}); err != nil {
				t.Fatal(err)
			}
			if message := receiveSocket(t, observer); message.Type != "pong" {
				t.Fatalf("failed reply was broadcast: %+v", message)
			}
		})
	}
}

func TestBotQueueIsBoundedAndCanceledOnShutdown(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	generator := botFunc(func(ctx context.Context, _ bot.Request) (bot.Response, error) {
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return bot.Response{}, ctx.Err()
	})
	server, handler := botServer(t, testStore(t), generator, time.Minute)
	conn, _ := connectChat(t, server)
	sendChat(t, conn, "Ada", "/bot first")
	receiveSocket(t, conn)
	receiveBotStatus(t, conn, "queued")
	receiveBotStatus(t, conn, "running")
	<-started
	for range 8 {
		sendChat(t, conn, "Ada", "/bot queued")
		receiveSocket(t, conn)
		receiveBotStatus(t, conn, "queued")
	}
	sendChat(t, conn, "Ada", "/bot overflow")
	receiveSocket(t, conn)
	if message := receiveSocket(t, conn); message.Type != "error" || !strings.Contains(message.Message, "queue is full") {
		t.Fatalf("queue was unbounded: %+v", message)
	}
	handler.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel active bot")
	}
	select {
	case <-started:
		t.Fatal("queued job ran during shutdown")
	default:
	}
}

func TestBotCommandValidationAndDisabledMode(t *testing.T) {
	_, server, _ := socketServer(t)
	conn, _ := connectChat(t, server)
	for _, message := range []string{"/botanical hello", "/bot", "/bot hello"} {
		sendChat(t, conn, "Ada", message)
		if echoed := receiveSocket(t, conn); echoed.Type != "chat" {
			t.Fatal("message was not persisted")
		}
		if message != "/botanical hello" {
			if reply := receiveSocket(t, conn); reply.Type != "error" {
				t.Fatalf("missing command feedback: %+v", reply)
			}
		}
	}
}

func TestRealSidecarBinaryThroughChat(t *testing.T) {
	// Use a local fake provider, but the real API -> binary -> CLI protocol.
	sidecar := filepath.Join(t.TempDir(), "bot-sidecar")
	build := exec.Command("go", "build", "-o", sidecar, "../../cmd/bot-sidecar")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build sidecar: %s, %v", output, err)
	}
	provider := filepath.Join(t.TempDir(), "fake-codex")
	if err := os.WriteFile(provider, []byte("#!/bin/sh\ncat >/dev/null\nprintf 'Real sidecar integration reply\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOT_CODEX_BIN", provider)
	server, _ := botServer(t, testStore(t), bot.Client{Path: sidecar}, 5*time.Second)
	conn, _ := connectChat(t, server)
	sendChat(t, conn, "Ada", "/bot codex Hello")
	receiveSocket(t, conn)
	receiveBotStatus(t, conn, "queued")
	receiveBotStatus(t, conn, "running")
	if reply := receiveSocket(t, conn); reply.Provider != "codex" || reply.Message != "Real sidecar integration reply" {
		t.Fatalf("sidecar not integrated: %+v", reply)
	}
	receiveBotStatus(t, conn, "done")
}
