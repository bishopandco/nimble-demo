package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go-api/internal/model"
)

const callColumns = `prompt_id, owner_pid, provider, status, queued_at, started_at, finished_at,
    pid, provider_pid, memory_bytes, cpu_percent, process_count, sampled_at, error`

type rowScanner interface{ Scan(...any) error }

func scanCall(row rowScanner) (model.BotCall, error) {
	var call model.BotCall
	var queued string
	var started, finished, sampled sql.NullString
	var memory sql.NullInt64
	var cpu sql.NullFloat64
	err := row.Scan(&call.ID, &call.OwnerPID, &call.Provider, &call.Status, &queued, &started, &finished,
		&call.PID, &call.ProviderPID, &memory, &cpu, &call.ProcessCount, &sampled, &call.Error)
	if err != nil {
		return call, err
	}
	call.QueuedAt, err = time.Parse(time.RFC3339Nano, queued)
	if err != nil {
		return call, err
	}
	for _, field := range []struct {
		value sql.NullString
		dest  **time.Time
	}{{started, &call.StartedAt}, {finished, &call.FinishedAt}, {sampled, &call.SampledAt}} {
		if field.value.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, field.value.String)
			if parseErr != nil {
				return call, parseErr
			}
			*field.dest = &value
		}
	}
	if memory.Valid {
		call.MemoryBytes = &memory.Int64
	}
	if cpu.Valid {
		call.CPUPercent = &cpu.Float64
	}
	return call.At(time.Now().UTC()), nil
}

func callEvent(ctx context.Context, tx *sql.Tx, id int64, kind string) error {
	call, err := scanCall(tx.QueryRowContext(ctx, "SELECT "+callColumns+" FROM bot_calls WHERE prompt_id = ?", id))
	if err != nil {
		return err
	}
	data, err := json.Marshal(call)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO bot_call_events (call_id, kind, occurred_at, data) VALUES (?, ?, ?, ?)", id, kind, time.Now().UTC().Format(time.RFC3339Nano), string(data))
	return err
}

func (s *Store) QueueBotCall(ctx context.Context, id int64, owner int, provider string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "INSERT INTO bot_calls (prompt_id, owner_pid, provider, status, queued_at) VALUES (?, ?, ?, 'queued', ?)", id, owner, provider, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if err := callEvent(ctx, tx, id, "queued"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) updateCall(ctx context.Context, id int64, kind, query string, args ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if err := callEvent(ctx, tx, id, kind); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) StartBotCall(ctx context.Context, id int64) error {
	return s.updateCall(ctx, id, "started", "UPDATE bot_calls SET status = 'running', started_at = ? WHERE prompt_id = ? AND status = 'queued'", time.Now().UTC().Format(time.RFC3339Nano), id)
}

func (s *Store) SetBotCallPID(ctx context.Context, id int64, pid int) error {
	return s.updateCall(ctx, id, "process_started", "UPDATE bot_calls SET pid = ? WHERE prompt_id = ? AND status = 'running'", pid, id)
}

func (s *Store) FinishBotCall(ctx context.Context, id int64, status, provider, message string) error {
	if status != "completed" && status != "failed" && status != "canceled" && status != "interrupted" {
		return fmt.Errorf("invalid final call status")
	}
	if len(message) > 512 {
		message = message[:512]
	}
	return s.updateCall(ctx, id, status, `UPDATE bot_calls SET status = ?, finished_at = ?, error = ?,
        provider = CASE WHEN ? IN ('codex', 'claude') THEN ? ELSE provider END
        WHERE prompt_id = ? AND status IN ('queued', 'running')`,
		status, time.Now().UTC().Format(time.RFC3339Nano), message, provider, provider, id)
}

func (s *Store) SampleBotCall(ctx context.Context, call model.BotCall) error {
	return s.updateCall(ctx, call.ID, "sample", `UPDATE bot_calls SET provider_pid = ?, memory_bytes = ?, cpu_percent = ?, process_count = ?, sampled_at = ?,
        provider = CASE WHEN ? IN ('codex', 'claude') THEN ? ELSE provider END
        WHERE prompt_id = ? AND status = 'running'`, call.ProviderPID, call.MemoryBytes, call.CPUPercent, call.ProcessCount,
		time.Now().UTC().Format(time.RFC3339Nano), call.Provider, call.Provider, call.ID)
}

func (s *Store) BotCalls(ctx context.Context, limit int) ([]model.BotCall, error) {
	if limit <= 0 || limit > 200 {
		return nil, fmt.Errorf("call limit must be 1–200")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+callColumns+" FROM bot_calls ORDER BY CASE WHEN status IN ('queued', 'running') THEN 0 ELSE 1 END, prompt_id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	calls := make([]model.BotCall, 0)
	for rows.Next() {
		call, err := scanCall(rows)
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	return calls, rows.Err()
}

func (s *Store) BotCallEvents(ctx context.Context, limit int) ([]model.BotCallEvent, error) {
	if limit <= 0 || limit > 500 {
		return nil, fmt.Errorf("event limit must be 1–500")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id, call_id, kind, occurred_at, data FROM bot_call_events ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]model.BotCallEvent, 0)
	for rows.Next() {
		var event model.BotCallEvent
		var timestamp, data string
		if err := rows.Scan(&event.ID, &event.CallID, &event.Kind, &timestamp, &data); err != nil {
			return nil, err
		}
		event.Timestamp, err = time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &event.Call); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
