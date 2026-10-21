// Package model defines chat records and the WebSocket protocol.
package model

import "time"

// Connection identifies one accepted socket session, including across restarts.
type Connection struct {
	ID        uint64    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// ChatMessage is a durable message in the shared room.
type ChatMessage struct {
	ID        int64     `json:"id"`
	ClientID  uint64    `json:"client_id"`
	Name      string    `json:"name"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Provider  string    `json:"provider,omitempty"`
	ReplyTo   int64     `json:"reply_to,omitempty"`
}

// SocketInput contains only fields that clients may supply.
type SocketInput struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// SocketMessage is the outbound event envelope. Only chat events are persisted.
type SocketMessage struct {
	Type      string          `json:"type"`
	ID        int64           `json:"id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Message   string          `json:"message,omitempty"`
	Name      string          `json:"name,omitempty"`
	ClientID  uint64          `json:"client_id,omitempty"`
	History   []SocketMessage `json:"history,omitempty"`
	Provider  string          `json:"provider,omitempty"`
	ReplyTo   int64           `json:"reply_to,omitempty"`
	State     string          `json:"state,omitempty"`
}

func (m ChatMessage) SocketMessage() SocketMessage {
	return SocketMessage{Type: "chat", ID: m.ID, ClientID: m.ClientID,
		Name: m.Name, Message: m.Message, Timestamp: m.Timestamp, Provider: m.Provider, ReplyTo: m.ReplyTo}
}
