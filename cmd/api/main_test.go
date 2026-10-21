package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-api/internal/api"
)

// Run the real entry point in a subprocess to test signal handling without
// signaling the test runner. HTTP Shutdown alone does not close WebSockets.
func TestServerWebSocketLifecycle(t *testing.T) {
	if os.Getenv("GO_API_TEST_SERVER") == "1" {
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	process := exec.Command(os.Args[0], "-test.run=^TestServerWebSocketLifecycle$")
	process.Env = append(os.Environ(), "GO_API_TEST_SERVER=1", "ADDR="+address, "DB_PATH="+filepath.Join(t.TempDir(), "blog.db"))
	process.Stderr = os.Stderr
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			process.Process.Kill()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("server process did not exit")
			}
		}
	})
	httpClient := &http.Client{Timeout: time.Second}
	base := "http://" + address
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err := httpClient.Get(base + "/health")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				break
			}
		}
		select {
		case err := <-done:
			stopped = true
			t.Fatalf("server exited before startup: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+address+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var message api.SocketMessage
	if err := conn.ReadJSON(&message); err != nil || message.Type != "ready" {
		t.Fatalf("ready: %+v, %v", message, err)
	}
	if err := conn.WriteJSON(map[string]string{"type": "ping"}); err != nil {
		t.Fatal(err)
	}
	if err := conn.ReadJSON(&message); err != nil || message.Type != "pong" {
		t.Fatalf("pong: %+v, %v", message, err)
	}
	response, err := httpClient.Post(base+"/tags", "application/json", strings.NewReader(`{"name":"Go"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 201 {
		t.Fatalf("create tag: %s, %v", body, err)
	}
	if err := conn.ReadJSON(&message); err != nil || message.Type != "created" || message.Resource != "tags" || message.ID != 1 {
		t.Fatalf("change event: %+v, %v", message, err)
	}
	if err := process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Fatalf("shutdown did not close socket cleanly: %v", err)
	}
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Fatalf("server shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
}
