package model

import "time"

// BotCall tracks one prompt's execution and latest process-tree sample.
type BotCall struct {
	ID           int64      `json:"id"`
	OwnerPID     int        `json:"owner_pid"`
	Provider     string     `json:"provider"`
	Status       string     `json:"status"`
	QueuedAt     time.Time  `json:"queued_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	PID          int        `json:"pid,omitempty"`
	ProviderPID  int        `json:"provider_pid,omitempty"`
	MemoryBytes  *int64     `json:"memory_bytes"`
	CPUPercent   *float64   `json:"cpu_percent"`
	ProcessCount int        `json:"process_count"`
	SampledAt    *time.Time `json:"sampled_at,omitempty"`
	ElapsedMS    int64      `json:"elapsed_ms"`
	Error        string     `json:"error,omitempty"`
}

func (c BotCall) Active() bool { return c.Status == "queued" || c.Status == "running" }

func (c BotCall) At(now time.Time) BotCall {
	if c.StartedAt != nil {
		end := now
		if c.FinishedAt != nil {
			end = *c.FinishedAt
		}
		c.ElapsedMS = max(0, end.Sub(*c.StartedAt).Milliseconds())
	}
	return c
}

type BotCallEvent struct {
	ID        int64     `json:"id"`
	CallID    int64     `json:"call_id"`
	Kind      string    `json:"kind"`
	Timestamp time.Time `json:"timestamp"`
	Call      BotCall   `json:"call"`
}

type MonitorSnapshot struct {
	Type      string         `json:"type"`
	Source    string         `json:"source"`
	Timestamp time.Time      `json:"timestamp"`
	Calls     []BotCall      `json:"calls"`
	Events    []BotCallEvent `json:"events"`
	Error     string         `json:"error,omitempty"`
}
