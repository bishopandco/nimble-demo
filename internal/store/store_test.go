package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go-api/internal/model"
)

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMessagesPersistAndHistoryIsBounded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "chat.db")
	db := openTestStore(t, path)
	connection, err := db.CreateConnection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sent []model.ChatMessage
	for i := 1; i <= 105; i++ {
		message, err := db.CreateMessage(ctx, connection.ID, "Ada", fmt.Sprintf("Message %d\n  second line", i))
		if err != nil {
			t.Fatal(err)
		}
		if message.ID != int64(i) || message.Timestamp.IsZero() {
			t.Fatalf("invalid record: %+v", message)
		}
		sent = append(sent, message)
	}
	unused, err := db.CreateConnection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, path)
	history, err := reopened.RecentMessages(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(history, sent[5:]) {
		t.Fatal("history changed after database reopen")
	}
	all, err := reopened.RecentMessages(ctx, 1000)
	if err != nil || !reflect.DeepEqual(all, sent) {
		t.Fatalf("older messages were not retained: %v", err)
	}
	identity, err := reopened.CreateConnection(ctx)
	if err != nil || identity.ID <= unused.ID {
		t.Fatalf("connection identity reused: %+v, %v", identity, err)
	}
	next, err := reopened.CreateMessage(ctx, identity.ID, "Grace", "After restart")
	if err != nil || next.ID != 106 {
		t.Fatalf("message ID did not continue: %+v, %v", next, err)
	}
}

func TestMessageConstraintsAndUnicode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestStore(t, ":memory:")
	connection, err := db.CreateConnection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, text string
		clientID   uint64
	}{
		{"", "hello", connection.ID},
		{strings.Repeat("a", 41), "hello", connection.ID},
		{"Ada", "", connection.ID},
		{"Ada", strings.Repeat("a", 501), connection.ID},
		{"Ada", "hello", connection.ID + 100},
	} {
		if _, err := db.CreateMessage(ctx, tc.clientID, tc.name, tc.text); err == nil {
			t.Fatalf("invalid record accepted: %+v", tc)
		}
	}
	if messages, err := db.RecentMessages(ctx, 100); err != nil || len(messages) != 0 {
		t.Fatalf("failed write persisted: %+v, %v", messages, err)
	}
	message, err := db.CreateMessage(ctx, connection.ID, strings.Repeat("界", 40), strings.Repeat("🌍", 500))
	if err != nil {
		t.Fatal(err)
	}
	history, err := db.RecentMessages(ctx, 100)
	if err != nil || !reflect.DeepEqual(history, []model.ChatMessage{message}) {
		t.Fatalf("Unicode message changed: %v", err)
	}
	if _, err := db.RecentMessages(ctx, 0); err == nil {
		t.Fatal("invalid history limit accepted")
	}
}

func TestSchemaPreservesUnrelatedData(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "chat.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("CREATE TABLE settings (value TEXT); INSERT INTO settings VALUES ('keep'); PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db := openTestStore(t, path)
	var value string
	if err := db.db.QueryRow("SELECT value FROM settings").Scan(&value); err != nil || value != "keep" {
		t.Fatalf("unrelated data changed: %q, %v", value, err)
	}
	if _, err := db.CreateConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if db, err := Open(ctx, ":memory:"); err == nil {
		db.Close()
		t.Fatal("canceled open succeeded")
	}
	if db, err := Open(context.Background(), t.TempDir()); err == nil {
		db.Close()
		t.Fatal("opened a directory as a database")
	}
}

func TestBotRepliesAreAtomicAndKeepPromptMetadata(t *testing.T) {
	db := openTestStore(t, ":memory:")
	ctx := context.Background()
	identity, err := db.CreateConnection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := db.CreateMessage(ctx, identity.ID, "Ada", "/bot Hello")
	if err != nil {
		t.Fatal(err)
	}
	for _, chunks := range [][]string{{"First chunk", strings.Repeat("x", 501)}, {"First chunk", ""}} {
		if _, err := db.CreateBotReply(ctx, identity.ID, "codex", prompt.ID, chunks); err == nil {
			t.Fatal("invalid reply committed")
		}
		if messages, err := db.RecentMessages(ctx, 100); err != nil || len(messages) != 1 {
			t.Fatalf("partial reply persisted: %+v, %v", messages, err)
		}
	}
	if _, err := db.CreateBotReply(ctx, identity.ID, "unknown", prompt.ID, []string{"reply"}); err == nil {
		t.Fatal("unknown provider committed")
	}
	messages, err := db.CreateBotReply(ctx, identity.ID, "claude", prompt.ID, []string{"First", "Second"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := db.RecentMessages(ctx, 100)
	if err != nil || len(history) != 3 || !reflect.DeepEqual(history[1:], messages) || history[1].ReplyTo != prompt.ID || history[1].Provider != "claude" {
		t.Fatalf("metadata lost: %+v, %v", history, err)
	}
}
