package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"

	"go-api/internal/bot"
	"go-api/internal/model"
	"go-api/internal/monitor"
	"go-api/internal/store"
)

const (
	socketQueueSize  = 64
	socketReadLimit  = 4096
	socketWriteWait  = 5 * time.Second
	socketPongWait   = 60 * time.Second
	socketPingEvery  = 25 * time.Second
	chatHistoryLimit = 100
	chatTextLimit    = 500
	chatNameLimit    = 40
)

// SocketMessage is the shared outbound protocol model.
type SocketMessage = model.SocketMessage

type socketHub struct {
	mu          sync.Mutex
	clients     map[*socketClient]struct{}
	closed      bool
	wg          sync.WaitGroup
	logger      *slog.Logger
	nextID      atomic.Uint64
	store       *store.Store
	history     []SocketMessage
	bot         bot.Generator
	botTimeout  time.Duration
	botQueue    chan botJob
	botCancel   context.CancelFunc
	botWG       sync.WaitGroup
	botClientID uint64
	pendingBots map[int64]struct{}
	monitor     *monitor.Service
}

type socketClient struct {
	conn   *websocket.Conn
	send   chan SocketMessage
	done   chan struct{}
	once   sync.Once
	reason string
	id     uint64
}

func newSocketHub(ctx context.Context, db *store.Store, logger *slog.Logger) (*socketHub, error) {
	saved, err := db.RecentMessages(ctx, chatHistoryLimit)
	if err != nil {
		return nil, err
	}
	hub := &socketHub{clients: make(map[*socketClient]struct{}), pendingBots: make(map[int64]struct{}), store: db, logger: logger.With("component", "websocket")}
	for _, message := range saved {
		hub.history = append(hub.history, message.SocketMessage())
	}
	return hub, nil
}

func (h *socketHub) serveHTTP(w http.ResponseWriter, r *http.Request) {
	connectionID := h.nextID.Add(1)
	logger := h.logger.With("connection_id", connectionID, "remote_addr", r.RemoteAddr)
	// Go's GET pattern also matches HEAD, which cannot perform an upgrade.
	if r.Method != http.MethodGet {
		logger.Warn("socket connection rejected", "reason", "method must be GET", "status", http.StatusMethodNotAllowed)
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, "WebSocket connections require GET")
		return
	}
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		logger.Warn("socket connection rejected", "reason", "server shutting down", "status", http.StatusServiceUnavailable)
		writeError(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	}
	upgrader := websocket.Upgrader{
		HandshakeTimeout: socketWriteWait,
		// The default origin policy permits absent Origin headers and matching hosts.
		Error: func(w http.ResponseWriter, r *http.Request, status int, reason error) {
			writeError(w, status, http.StatusText(status))
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Warn("socket connection rejected", "error", err)
		return
	}
	registrationCtx, cancelRegistration := context.WithTimeout(r.Context(), socketWriteWait)
	identity, err := h.store.CreateConnection(registrationCtx)
	cancelRegistration()
	if err != nil {
		logger.Error("socket registration failed", "error", err)
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "could not register connection"), time.Now().Add(socketWriteWait))
		_ = conn.Close()
		return
	}
	logger = logger.With("client_id", identity.ID)
	started := time.Now()
	var received int
	var closeCode int
	disconnectReason := "client disconnected"
	client := &socketClient{conn: conn, send: make(chan SocketMessage, socketQueueSize), done: make(chan struct{}), id: identity.ID}
	conn.SetReadLimit(socketReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(socketPongWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(socketPongWait)) })
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		logger.Info("socket connection closed", "reason", "server shutting down")
		client.close("server shutting down")
		return
	}
	// Queue ready before registration so history precedes every live message.
	pending := make([]int64, 0, len(h.pendingBots))
	for id := range h.pendingBots {
		pending = append(pending, id)
	}
	slices.Sort(pending)
	client.send <- SocketMessage{Type: "ready", ClientID: client.id, Timestamp: time.Now().UTC(), History: append([]SocketMessage(nil), h.history...), PendingBotIDs: pending}
	h.clients[client] = struct{}{}
	h.wg.Add(1)
	h.mu.Unlock()
	logger.Info("socket connected")
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		client.writePump()
	}()
	defer func() {
		client.close(disconnectReason)
		<-writerDone
		h.mu.Lock()
		delete(h.clients, client)
		h.mu.Unlock()
		attrs := []any{"reason", client.reason, "duration", time.Since(started), "messages_received", received}
		if closeCode != 0 {
			attrs = append(attrs, "close_code", closeCode)
		}
		logger.Info("socket disconnected", attrs...)
		h.wg.Done()
	}()
	for {
		kind, payload, err := conn.ReadMessage()
		if err != nil {
			disconnectReason = err.Error()
			if errors.Is(err, websocket.ErrReadLimit) {
				logger.Warn("socket message rejected", "reason", "message exceeds 4 KiB", "close_code", websocket.CloseMessageTooBig)
			}
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				closeCode = closeErr.Code
				if closeCode == websocket.CloseNormalClosure || closeCode == websocket.CloseGoingAway {
					disconnectReason = "peer closed connection"
				}
			}
			return
		}
		received++
		frameType := "text"
		if kind == websocket.BinaryMessage {
			frameType = "binary"
		}
		attrs := []any{"frame_type", frameType, "bytes", len(payload)}
		if kind == websocket.TextMessage && utf8.Valid(payload) {
			attrs = append(attrs, "payload", string(payload))
		}
		logger.Info("socket message received", attrs...)
		if kind != websocket.TextMessage {
			disconnectReason = "unsupported message type"
			logger.Warn("socket message rejected", "reason", disconnectReason, "close_code", websocket.CloseUnsupportedData)
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "send JSON text messages"), time.Now().Add(socketWriteWait))
			return
		}
		if !utf8.Valid(payload) {
			disconnectReason = "invalid UTF-8"
			logger.Warn("socket message rejected", "reason", disconnectReason, "close_code", websocket.CloseInvalidFramePayloadData)
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInvalidFramePayloadData, "invalid UTF-8"), time.Now().Add(socketWriteWait))
			return
		}
		message := SocketMessage{Type: "pong", Timestamp: time.Now().UTC()}
		var input model.SocketInput
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		validationError := ""
		if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF {
			validationError = "expected one JSON object with type ping or chat"
		} else {
			switch input.Type {
			case "ping":
				if input.Name != "" || input.Message != "" {
					validationError = "ping does not accept name or message"
				}
			case "chat":
				input.Name = strings.TrimSpace(input.Name)
				if input.Name == "" {
					input.Name = "Guest " + strconv.FormatUint(client.id, 10)
				}
				if err := text(&input.Name, "name", chatNameLimit); err != nil {
					validationError = err.Error()
				} else if err := text(&input.Message, "message", chatTextLimit); err != nil {
					validationError = err.Error()
				}
			default:
				validationError = "type must be ping or chat"
			}
		}
		if validationError == "" && input.Type == "chat" {
			h.chat(r.Context(), client, input.Name, input.Message)
			continue
		}
		if validationError != "" {
			logger.Warn("socket message rejected", "reason", validationError)
			message.Type = "error"
			message.Message = validationError
		}
		h.mu.Lock()
		h.enqueue(client, message)
		h.mu.Unlock()
	}
}

func (h *socketHub) chat(ctx context.Context, client *socketClient, name, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.clients[client]; h.closed || !exists {
		return
	}
	saveCtx, cancel := context.WithTimeout(ctx, socketWriteWait)
	defer cancel()
	saved, err := h.store.CreateMessage(saveCtx, client.id, name, text)
	if err != nil {
		h.logger.Error("chat message persistence failed", "client_id", client.id, "error", err)
		h.enqueue(client, SocketMessage{Type: "error", Message: "Message was not saved. Please try again.", Timestamp: time.Now().UTC()})
		return
	}
	h.publishChat(saved)
	request, command, err := bot.Parse(text)
	if !command {
		return
	}
	if err != nil {
		h.botError(client, saved.ID, err.Error())
		return
	}
	if h.bot == nil {
		h.botError(client, saved.ID, "Bot is disabled on this server.")
		return
	}
	if len(h.botQueue) == cap(h.botQueue) {
		h.botError(client, saved.ID, "Bot queue is full. Please try again shortly.")
		return
	}
	if h.monitor != nil {
		if err := h.monitor.Queue(ctx, saved.ID, request.Provider); err != nil {
			h.logger.Error("register monitored bot call", "prompt_id", saved.ID, "error", err)
			h.botError(client, saved.ID, "Bot call could not be registered. Please try again.")
			return
		}
	}
	// Producers hold mu, so only the worker can reduce this queue's length.
	h.botQueue <- botJob{request: request, promptID: saved.ID, sender: client}
	h.pendingBots[saved.ID] = struct{}{}
	h.broadcast(SocketMessage{Type: "bot_status", ID: saved.ID, State: "thinking", Message: "Bot is thinking…", Timestamp: time.Now().UTC()})
}

// broadcast requires mu. All chat messages use this one shared room.
func (h *socketHub) broadcast(message SocketMessage) {
	if h.closed {
		return
	}
	for client := range h.clients {
		h.enqueue(client, message)
	}
}

// enqueue requires mu. A slow connection cannot block other clients.
func (h *socketHub) enqueue(client *socketClient, message SocketMessage) {
	if _, ok := h.clients[client]; !ok {
		return
	}
	select {
	case client.send <- message:
	default:
		delete(h.clients, client)
		client.close("outgoing queue full")
	}
}

func (h *socketHub) close() {
	h.mu.Lock()
	h.closed = true
	if h.botCancel != nil {
		h.botCancel()
	}
	clients := make([]*socketClient, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.Unlock()
	// One shared deadline bounds close-frame writes across all connections.
	deadline := time.Now().Add(time.Second)
	for _, client := range clients {
		_ = client.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"), deadline)
		client.close("server shutting down")
	}
	h.wg.Wait()
	h.botWG.Wait()
}

func (c *socketClient) close(reason string) {
	c.once.Do(func() {
		c.reason = reason
		close(c.done)
		_ = c.conn.Close()
	})
}

func (c *socketClient) writePump() {
	ticker := time.NewTicker(socketPingEvery)
	defer ticker.Stop()
	defer c.close("writer stopped")
	for {
		select {
		case <-c.done:
			return
		case message := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(socketWriteWait))
			if err := c.conn.WriteJSON(message); err != nil {
				c.close("write failed: " + err.Error())
				return
			}
		case <-ticker.C:
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(socketWriteWait)); err != nil {
				c.close("heartbeat write failed: " + err.Error())
				return
			}
		}
	}
}
