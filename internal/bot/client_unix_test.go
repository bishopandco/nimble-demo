//go:build unix

package bot

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"go-api/internal/store"
)

func TestSidecarTimeoutStopsProviderProcess(t *testing.T) {
	sidecar := filepath.Join(t.TempDir(), "bot-sidecar")
	build := exec.Command("go", "build", "-o", sidecar, "../../cmd/bot-sidecar")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build sidecar: %s, %v", output, err)
	}
	provider := filepath.Join(t.TempDir(), "stubborn-provider")
	pidFile := filepath.Join(t.TempDir(), "provider.pid")
	script := "#!/bin/sh\ntrap '' TERM\necho $$ > \"$GO_BOT_PID_FILE\"\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(provider, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOT_CODEX_BIN", provider)
	t.Setenv("GO_BOT_PID_FILE", pidFile)
	t.Setenv("BOT_PROMPT_FILE", "")
	path := filepath.Join(t.TempDir(), "chat.db")
	t.Setenv("DB_PATH", path)
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	connection, err := db.CreateConnection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := db.CreateMessage(context.Background(), connection.ID, "Ada", "/bot codex Hello")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = (Client{Path: sidecar}).Generate(ctx, Request{Provider: "codex", Prompt: "Hello", MessageID: prompt.ID})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected timeout: %v", err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("provider did not start: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		t.Fatalf("provider process survived sidecar cancellation: %v", err)
	}
}
