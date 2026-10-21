// Package api serves the blog REST API using net/http.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"go-api/internal/store"
)

// Server serves HTTP requests and owns its WebSocket connections.
type Server struct {
	handler http.Handler
	sockets *socketHub
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// Close disconnects WebSocket clients and waits for their handlers to finish.
// The caller still owns the store's lifetime.
func (s *Server) Close() { s.sockets.close() }

// New builds an HTTP handler. Call Close when shutting down the server.
func New(db *store.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()
	sockets := newSocketHub(logger)
	mux.HandleFunc("GET /ws", sockets.serveHTTP)
	register(mux, logger, sockets, "/users", "", validateUser, db.ListUsers, db.GetUser, db.CreateUser, db.UpdateUser, db.DeleteUser)
	register(mux, logger, sockets, "/blogs", "tag_id", validateBlog, db.ListBlogs, db.GetBlog, db.CreateBlog, db.UpdateBlog, db.DeleteBlog)
	register(mux, logger, sockets, "/comments", "blog_id", validateComment, db.ListComments, db.GetComment, db.CreateComment, db.UpdateComment, db.DeleteComment)
	register(mux, logger, sockets, "/tags", "", validateTag, db.ListTags, db.GetTag, db.CreateTag, db.UpdateTag, db.DeleteTag)
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
	return &Server{handler: handler, sockets: sockets}
}

// register shares HTTP mechanics while keeping payloads and store methods typed.
func register[Input any, Resource interface{ ResourceID() int64 }](
	mux *http.ServeMux, logger *slog.Logger, sockets *socketHub, path, filter string,
	validate func(*Input) error,
	list func(context.Context, store.ListOptions) ([]Resource, error),
	get func(context.Context, int64) (Resource, error),
	create func(context.Context, Input) (Resource, error),
	update func(context.Context, int64, Input) (Resource, error),
	remove func(context.Context, int64) error,
) {
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		opts, err := listOptions(r, filter)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resources, err := list(r.Context(), opts)
		if err != nil {
			handleError(w, logger, err)
			return
		}
		writeJSON(w, http.StatusOK, resources)
	})
	mux.HandleFunc("GET "+path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := resourceID(w, r)
		if !ok {
			return
		}
		resource, err := get(r.Context(), id)
		if err != nil {
			handleError(w, logger, err)
			return
		}
		writeJSON(w, http.StatusOK, resource)
	})
	write := func(w http.ResponseWriter, r *http.Request) {
		var id int64
		if r.Method == http.MethodPut {
			var ok bool
			id, ok = resourceID(w, r)
			if !ok {
				return
			}
		}
		var input Input
		if !decode(w, r, &input) {
			return
		}
		if err := validate(&input); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var resource Resource
		var err error
		status := http.StatusCreated
		if r.Method == http.MethodPost {
			resource, err = create(r.Context(), input)
		} else {
			status = http.StatusOK
			resource, err = update(r.Context(), id, input)
		}
		if err != nil {
			handleError(w, logger, err)
			return
		}
		if status == http.StatusCreated {
			w.Header().Set("Location", fmt.Sprintf("%s/%d", path, resource.ResourceID()))
		}
		action := "updated"
		if status == http.StatusCreated {
			action = "created"
		}
		sockets.publish(action, strings.TrimPrefix(path, "/"), resource.ResourceID())
		writeJSON(w, status, resource)
	}
	mux.HandleFunc("POST "+path, write)
	mux.HandleFunc("PUT "+path+"/{id}", write)
	mux.HandleFunc("DELETE "+path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := resourceID(w, r)
		if !ok {
			return
		}
		if err := remove(r.Context(), id); err != nil {
			handleError(w, logger, err)
			return
		}
		sockets.publish("deleted", strings.TrimPrefix(path, "/"), id)
		w.WriteHeader(http.StatusNoContent)
	})
}

func resourceID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

func listOptions(r *http.Request, filter string) (store.ListOptions, error) {
	opts := store.ListOptions{Limit: 20}
	query := r.URL.Query()
	for _, field := range []struct {
		name string
		dest *int
		min  int
		max  int
	}{{"limit", &opts.Limit, 1, 100}, {"offset", &opts.Offset, 0, 1_000_000}} {
		if query.Has(field.name) {
			value, err := strconv.Atoi(query.Get(field.name))
			if err != nil || value < field.min || value > field.max {
				return opts, fmt.Errorf("%s must be between %d and %d", field.name, field.min, field.max)
			}
			*field.dest = value
		}
	}
	if filter != "" && query.Has(filter) {
		id, err := strconv.ParseInt(query.Get(filter), 10, 64)
		if err != nil || id <= 0 {
			return opts, fmt.Errorf("%s must be a positive integer", filter)
		}
		opts.RelatedID = id
	}
	return opts, nil
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		decodeError(w, err)
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("body must contain one JSON object")
		}
		decodeError(w, err)
		return false
	}
	return true
}

func decodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "body exceeds 1 MiB")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON body")
}

func handleError(w http.ResponseWriter, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		logger.Error("request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
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
