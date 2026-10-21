package model

import "time"

// BotMemory is a shared room note, attributed to the prompt that last changed it.
type BotMemory struct {
	Key             string    `json:"key"`
	Content         string    `json:"content"`
	SourceMessageID int64     `json:"source_message_id"`
	UpdatedAt       time.Time `json:"updated_at"`
}
