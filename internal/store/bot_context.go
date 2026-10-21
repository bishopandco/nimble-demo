package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"go-api/internal/model"
)

// SearchMessages pages backward through SQLite, returning chronological results.
// Bounds are exclusive; zero means unbounded. Search is a literal substring.
func (s *Store) SearchMessages(ctx context.Context, before, after int64, query string, limit int) ([]model.ChatMessage, error) {
	if before < 0 || after < 0 || limit < 1 || limit > 20 || !utf8.ValidString(query) || utf8.RuneCountInString(query) > 200 {
		return nil, fmt.Errorf("history requires nonnegative cursors, a search of at most 200 characters, and a limit of 1–20")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.client_id, m.name, m.message, m.created_at,
        COALESCE(b.provider, ''), COALESCE(b.prompt_id, 0)
        FROM chat_messages m LEFT JOIN chat_bot_messages b ON b.message_id = m.id
        WHERE (? = 0 OR m.id < ?) AND m.id > ?
          AND (? = '' OR instr(lower(m.message), lower(?)) > 0)
        ORDER BY m.id DESC LIMIT ?`, before, before, after, query, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := make([]model.ChatMessage, 0)
	for rows.Next() {
		var message model.ChatMessage
		var timestamp string
		if err := rows.Scan(&message.ID, &message.ClientID, &message.Name, &message.Message, &timestamp, &message.Provider, &message.ReplyTo); err != nil {
			return nil, err
		}
		message.Timestamp, err = time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	slices.Reverse(messages)
	return messages, rows.Err()
}

func (s *Store) BotMemories(ctx context.Context) ([]model.BotMemory, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, content, source_message_id, updated_at FROM bot_memory ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := make([]model.BotMemory, 0)
	for rows.Next() {
		var note model.BotMemory
		var timestamp string
		if err := rows.Scan(&note.Key, &note.Content, &note.SourceMessageID, &timestamp); err != nil {
			return nil, err
		}
		note.UpdatedAt, err = time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	return notes, rows.Err()
}

func memoryKey(key string) bool {
	if len(key) < 1 || len(key) > 80 {
		return false
	}
	for _, char := range key {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || strings.ContainsRune("._/-", char)) {
			return false
		}
	}
	return true
}

func (s *Store) RememberBotMemory(ctx context.Context, key, content string, source int64) error {
	content = strings.TrimSpace(content)
	if !memoryKey(key) || !utf8.ValidString(content) || content == "" || utf8.RuneCountInString(content) > 1000 || source <= 0 {
		return fmt.Errorf("memory requires a lowercase key of 1–80 letters, digits, ._/- and content of 1–1000 characters")
	}
	// The bound and upsert share one write statement, including under concurrent writers.
	result, err := s.db.ExecContext(ctx, `INSERT INTO bot_memory (key, content, source_message_id, updated_at)
        SELECT ?, ?, ?, ? WHERE (SELECT count(*) FROM bot_memory) < 32 OR EXISTS (SELECT 1 FROM bot_memory WHERE key = ?)
        ON CONFLICT(key) DO UPDATE SET content = excluded.content, source_message_id = excluded.source_message_id, updated_at = excluded.updated_at`,
		key, content, source, time.Now().UTC().Format(time.RFC3339Nano), key)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return fmt.Errorf("memory is full (32 notes); update or forget an existing note first")
	}
	return err
}

func (s *Store) ForgetBotMemory(ctx context.Context, key string) error {
	if !memoryKey(key) {
		return fmt.Errorf("invalid memory key")
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM bot_memory WHERE key = ?", key)
	return err
}
