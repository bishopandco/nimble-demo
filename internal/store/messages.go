package store

import (
	"context"
	"fmt"
	"time"

	"go-api/internal/model"
)

func (s *Store) CreateConnection(ctx context.Context) (model.Connection, error) {
	connection := model.Connection{CreatedAt: time.Now().UTC()}
	result, err := s.db.ExecContext(ctx, "INSERT INTO chat_connections (created_at) VALUES (?)", connection.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return model.Connection{}, err
	}
	id, err := result.LastInsertId()
	connection.ID = uint64(id)
	return connection, err
}

// CreateMessage commits a message before returning its database-assigned ID.
func (s *Store) CreateMessage(ctx context.Context, clientID uint64, name, text string) (model.ChatMessage, error) {
	message := model.ChatMessage{ClientID: clientID, Name: name, Message: text, Timestamp: time.Now().UTC()}
	result, err := s.db.ExecContext(ctx,
		"INSERT INTO chat_messages (client_id, name, message, created_at) VALUES (?, ?, ?, ?)",
		message.ClientID, message.Name, message.Message, message.Timestamp.Format(time.RFC3339Nano))
	if err != nil {
		return model.ChatMessage{}, err
	}
	message.ID, err = result.LastInsertId()
	return message, err
}

// RecentMessages returns the newest limit records in chronological ID order.
// Older messages remain in the database.
func (s *Store) RecentMessages(ctx context.Context, limit int) ([]model.ChatMessage, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("message limit must be positive")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.client_id, m.name, m.message, m.created_at,
        COALESCE(b.provider, ''), COALESCE(b.prompt_id, 0) FROM (
        SELECT id, client_id, name, message, created_at FROM chat_messages ORDER BY id DESC LIMIT ?
    ) m LEFT JOIN chat_bot_messages b ON b.message_id = m.id ORDER BY m.id`, limit)
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
			return nil, fmt.Errorf("read message timestamp: %w", err)
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

// CreateBotReply commits all reply chunks and their prompt links as one unit.
func (s *Store) CreateBotReply(ctx context.Context, clientID uint64, provider string, promptID int64, chunks []string) ([]model.ChatMessage, error) {
	if len(chunks) == 0 || len(chunks) > 32 {
		return nil, fmt.Errorf("bot reply must contain 1–32 chunks")
	}
	name := "Codex"
	if provider == "claude" {
		name = "Claude"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	messages := make([]model.ChatMessage, 0, len(chunks))
	for _, text := range chunks {
		message := model.ChatMessage{ClientID: clientID, Name: name, Message: text, Timestamp: time.Now().UTC(), Provider: provider, ReplyTo: promptID}
		result, err := tx.ExecContext(ctx, "INSERT INTO chat_messages (client_id, name, message, created_at) VALUES (?, ?, ?, ?)", clientID, name, text, message.Timestamp.Format(time.RFC3339Nano))
		if err != nil {
			return nil, err
		}
		message.ID, err = result.LastInsertId()
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO chat_bot_messages (message_id, prompt_id, provider) VALUES (?, ?, ?)", message.ID, promptID, provider); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return messages, nil
}
