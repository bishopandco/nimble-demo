// Package api serves the shared chatroom over HTTP and WebSockets.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"go-api/internal/store"
)

// Server serves HTTP requests and owns its WebSocket connections.
type Server struct {
	handler http.Handler
	sockets *socketHub
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// Close disconnects WebSocket clients and waits for their handlers to finish.
func (s *Server) Close() {
	s.sockets.close()
	if s.sockets.monitor != nil {
		s.sockets.monitor.Close()
	}
}

// New restores chat history and builds an HTTP handler. Close it before the store.
func New(ctx context.Context, db *store.Store, logger *slog.Logger, options ...Option) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()
	sockets, err := newSocketHub(ctx, db, logger)
	if err != nil {
		return nil, err
	}
	for _, option := range options {
		option(sockets)
	}
	if sockets.monitor != nil {
		sockets.monitor.Start(ctx)
		mux.Handle("GET /ws/monitor", sockets.monitor)
	} else {
		mux.HandleFunc("GET /ws/monitor", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "bot monitor is disabled")
		})
	}
	if sockets.bot != nil {
		sockets.startBot(ctx)
	}
	mux.HandleFunc("GET /ws", sockets.serveHTTP)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	// Preserve ServeMux's method matching and Allow header while making errors JSON.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		handler, pattern := mux.Handler(r)
		if pattern == "" {
			recorder := &routeErrorWriter{header: make(http.Header)}
			handler.ServeHTTP(recorder, r)
			if allow := recorder.header.Get("Allow"); allow != "" {
				w.Header().Set("Allow", allow)
			}
			writeError(w, recorder.status, http.StatusText(recorder.status))
			return
		}
		mux.ServeHTTP(w, r)
	})
	return &Server{handler: handler, sockets: sockets}, nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type routeErrorWriter struct {
	header http.Header
	status int
}

func (w *routeErrorWriter) Header() http.Header    { return w.header }
func (w *routeErrorWriter) WriteHeader(status int) { w.status = status }
func (w *routeErrorWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return len(p), nil
}
