package api

import (
	"context"
	"errors"
	"time"

	"go-api/internal/bot"
	"go-api/internal/model"
)

// Option configures an optional server component.
type Option func(*socketHub)

func WithBot(generator bot.Generator, timeout time.Duration) Option {
	return func(h *socketHub) { h.bot, h.botTimeout = generator, timeout }
}

type botJob struct {
	request  bot.Request
	promptID int64
	sender   *socketClient
}

func (h *socketHub) startBot(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	h.botCancel = cancel
	h.botQueue = make(chan botJob, 8)
	if h.botTimeout <= 0 {
		h.botTimeout = 2 * time.Minute
	}
	h.botWG.Add(1)
	go func() {
		defer h.botWG.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case job := <-h.botQueue:
				if ctx.Err() != nil {
					return
				}
				h.runBot(ctx, job)
			}
		}
	}()
}

func (h *socketHub) runBot(ctx context.Context, job botJob) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.broadcast(SocketMessage{Type: "bot_status", ID: job.promptID, State: "running", Timestamp: time.Now().UTC()})
	h.mu.Unlock()

	requestCtx, cancel := context.WithTimeout(ctx, h.botTimeout)
	response, err := h.bot.Generate(requestCtx, job.request)
	cancel()
	if ctx.Err() != nil {
		return
	}
	if err == nil && (response.Output == "" || (response.Provider != "codex" && response.Provider != "claude")) {
		err = errors.New("bot returned an invalid response")
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	saveFailed := false
	if err == nil {
		saveCtx, cancel := context.WithTimeout(ctx, socketWriteWait)
		defer cancel()
		if h.botClientID == 0 {
			identity, createErr := h.store.CreateConnection(saveCtx)
			err = createErr
			h.botClientID = identity.ID
		}
		if err == nil {
			var messages []model.ChatMessage
			messages, err = h.store.CreateBotReply(saveCtx, h.botClientID, response.Provider, job.promptID, bot.Chunks(response.Output))
			if err == nil {
				for _, message := range messages {
					h.publishChat(message)
				}
			}
		}
		saveFailed = err != nil
	}
	state := "done"
	if err != nil {
		state = "failed"
		h.logger.Error("bot request failed", "prompt_id", job.promptID, "error", err)
		message := err.Error()
		if saveFailed {
			message = "Bot reply could not be saved. Please try again."
		}
		if errors.Is(err, context.DeadlineExceeded) {
			message = "Bot request timed out. Please try again."
		}
		h.botError(job.sender, message)
	}
	h.broadcast(SocketMessage{Type: "bot_status", ID: job.promptID, State: state, Timestamp: time.Now().UTC()})
}

// publishChat requires mu and accepts only committed records.
func (h *socketHub) publishChat(saved model.ChatMessage) {
	message := saved.SocketMessage()
	h.history = append(h.history, message)
	if len(h.history) > chatHistoryLimit {
		h.history = h.history[len(h.history)-chatHistoryLimit:]
	}
	h.broadcast(message)
}

func (h *socketHub) botError(client *socketClient, message string) {
	h.enqueue(client, SocketMessage{Type: "error", Message: message, Timestamp: time.Now().UTC()})
}
