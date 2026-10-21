package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-api/internal/api"
	"go-api/internal/store"
)

type client struct {
	t *testing.T
	h http.Handler
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func setup(t *testing.T) *client {
	t.Helper()
	db := testStore(t)
	handler, err := api.New(context.Background(), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handler.Close)
	return &client{t: t, h: handler}
}

func (c *client) request(method, path, body string, status int) *httptest.ResponseRecorder {
	c.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	if w.Code != status {
		c.t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	if status != http.StatusNoContent {
		if w.Header().Get("Content-Type") != "application/json" {
			c.t.Fatalf("missing JSON content type: %v", w.Header())
		}
		if !json.Valid(w.Body.Bytes()) {
			c.t.Fatalf("invalid JSON response: %s", w.Body.String())
		}
	}
	return w
}

func read[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestHealth(t *testing.T) {
	t.Parallel()
	c := setup(t)
	response := c.request("GET", "/health", "", http.StatusOK)
	if status := read[map[string]string](t, response); status["status"] != "ok" {
		t.Fatalf("unexpected health: %v", status)
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing content type protection")
	}
}

func TestRemovedRoutes(t *testing.T) {
	t.Parallel()
	c := setup(t)
	for _, path := range []string{"/users", "/blogs", "/comments", "/tags"} {
		for _, suffix := range []string{"", "/1"} {
			for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
				response := c.request(method, path+suffix, "", http.StatusNotFound)
				if error := read[map[string]string](t, response); error["error"] != "Not Found" {
					t.Fatalf("unexpected route error: %v", error)
				}
			}
		}
	}
}

func TestRouteErrors(t *testing.T) {
	t.Parallel()
	c := setup(t)
	c.request("GET", "/does-not-exist", "", http.StatusNotFound)
	response := c.request("POST", "/health", "", http.StatusMethodNotAllowed)
	if allow := response.Header().Get("Allow"); !strings.Contains(allow, "GET") {
		t.Fatalf("missing Allow header: %q", allow)
	}
}

func TestDefaultLogger(t *testing.T) {
	handler, err := api.New(context.Background(), testStore(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	c := &client{t: t, h: handler}
	c.request("GET", "/health", "", http.StatusOK)
}
