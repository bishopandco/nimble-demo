package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-api/internal/api"
	"go-api/internal/model"
	"go-api/internal/store"
)

type client struct {
	t *testing.T
	h http.Handler
}

func setup(t *testing.T) *client {
	t.Helper()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	handler := api.New(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

func (c *client) seed() (model.User, model.Tag, model.Blog) {
	c.t.Helper()
	u := read[model.User](c.t, c.request("POST", "/users", `{"name":"Ada","email":"ada@example.com"}`, 201))
	tag := read[model.Tag](c.t, c.request("POST", "/tags", `{"name":"Go"}`, 201))
	body := fmt.Sprintf(`{"title":"First post","body":"Hello","author_id":%d,"tag_ids":[%d]}`, u.ID, tag.ID)
	b := read[model.Blog](c.t, c.request("POST", "/blogs", body, 201))
	return u, tag, b
}

func TestCRUD(t *testing.T) {
	t.Parallel()
	c := setup(t)
	userResponse := c.request("POST", "/users", `{"name":" Ada ","email":" ADA@EXAMPLE.COM "}`, 201)
	u := read[model.User](t, userResponse)
	if u.Name != "Ada" || u.Email != "ada@example.com" || u.ID <= 0 {
		t.Fatalf("unexpected user: %+v", u)
	}
	if userResponse.Header().Get("Location") != fmt.Sprintf("/users/%d", u.ID) {
		t.Fatal("missing user Location")
	}
	if _, err := time.Parse(time.RFC3339Nano, u.CreatedAt); err != nil {
		t.Fatal(err)
	}
	userPath := fmt.Sprintf("/users/%d", u.ID)
	c.request("GET", userPath, "", 200)
	updatedUser := read[model.User](t, c.request("PUT", userPath, `{"name":"Grace","email":"grace@example.com"}`, 200))
	if updatedUser.Name != "Grace" || updatedUser.CreatedAt != u.CreatedAt {
		t.Fatalf("unexpected updated user: %+v", updatedUser)
	}

	tag := read[model.Tag](t, c.request("POST", "/tags", `{"name":" Go "}`, 201))
	tagPath := fmt.Sprintf("/tags/%d", tag.ID)
	c.request("GET", tagPath, "", 200)
	updatedTag := read[model.Tag](t, c.request("PUT", tagPath, `{"name":"Golang"}`, 200))
	if updatedTag.Name != "Golang" {
		t.Fatal(updatedTag)
	}

	blogBody := fmt.Sprintf(`{"title":"First","body":"Hello","author_id":%d,"tag_ids":[%d]}`, u.ID, tag.ID)
	blogResponse := c.request("POST", "/blogs", blogBody, 201)
	b := read[model.Blog](t, blogResponse)
	if len(b.Tags) != 1 || b.Tags[0] != updatedTag {
		t.Fatalf("missing tag: %+v", b)
	}
	blogPath := fmt.Sprintf("/blogs/%d", b.ID)
	if blogResponse.Header().Get("Location") != blogPath {
		t.Fatal("missing blog Location")
	}
	gotBlog := read[model.Blog](t, c.request("GET", blogPath, "", 200))
	if gotBlog.Title != "First" || gotBlog.AuthorID != u.ID || len(gotBlog.Tags) != 1 {
		t.Fatal(gotBlog)
	}

	commentBody := fmt.Sprintf(`{"blog_id":%d,"author_id":%d,"body":"Nice post"}`, b.ID, u.ID)
	comment := read[model.Comment](t, c.request("POST", "/comments", commentBody, 201))
	commentPath := fmt.Sprintf("/comments/%d", comment.ID)
	c.request("GET", commentPath, "", 200)
	commentBody = fmt.Sprintf(`{"blog_id":%d,"author_id":%d,"body":"Updated comment"}`, b.ID, u.ID)
	updatedComment := read[model.Comment](t, c.request("PUT", commentPath, commentBody, 200))
	if updatedComment.Body != "Updated comment" || updatedComment.CreatedAt != comment.CreatedAt {
		t.Fatal(updatedComment)
	}

	blogBody = fmt.Sprintf(`{"title":"Updated","body":"New text","author_id":%d,"tag_ids":[]}`, u.ID)
	updatedBlog := read[model.Blog](t, c.request("PUT", blogPath, blogBody, 200))
	if updatedBlog.Title != "Updated" || updatedBlog.Body != "New text" || len(updatedBlog.Tags) != 0 || updatedBlog.Tags == nil || updatedBlog.CreatedAt != b.CreatedAt {
		t.Fatal(updatedBlog)
	}
	for _, path := range []string{"/users", "/blogs", "/comments", "/tags"} {
		items := read[[]json.RawMessage](t, c.request("GET", path, "", 200))
		if len(items) != 1 {
			t.Fatalf("%s: expected one item, got %d", path, len(items))
		}
	}
	for _, path := range []string{commentPath, blogPath, tagPath, userPath} {
		w := c.request("DELETE", path, "", 204)
		if w.Body.Len() != 0 {
			t.Fatal("204 response has a body")
		}
		c.request("GET", path, "", 404)
		c.request("DELETE", path, "", 404)
	}
}

func TestConflictsAndCascades(t *testing.T) {
	t.Parallel()
	c := setup(t)
	u, tag, b := c.seed()
	c.request("POST", "/users", `{"name":"Other","email":"ADA@example.com"}`, 409)
	c.request("POST", "/tags", `{"name":"go"}`, 409)
	otherUser := read[model.User](t, c.request("POST", "/users", `{"name":"Other","email":"other@example.com"}`, 201))
	c.request("PUT", fmt.Sprintf("/users/%d", otherUser.ID), `{"name":"Other","email":"ADA@example.com"}`, 409)
	otherTag := read[model.Tag](t, c.request("POST", "/tags", `{"name":"SQLite"}`, 201))
	c.request("PUT", fmt.Sprintf("/tags/%d", otherTag.ID), `{"name":"go"}`, 409)
	commentBody := fmt.Sprintf(`{"blog_id":%d,"author_id":%d,"body":"Hello"}`, b.ID, u.ID)
	comment := read[model.Comment](t, c.request("POST", "/comments", commentBody, 201))
	c.request("DELETE", fmt.Sprintf("/users/%d", u.ID), "", 409)
	c.request("POST", "/blogs", `{"title":"Bad author","body":"Hello","author_id":9999}`, 409)
	c.request("POST", "/comments", fmt.Sprintf(`{"blog_id":9999,"author_id":%d,"body":"Hello"}`, u.ID), 409)
	c.request("POST", "/comments", fmt.Sprintf(`{"blog_id":%d,"author_id":9999,"body":"Hello"}`, b.ID), 409)
	c.request("PUT", fmt.Sprintf("/comments/%d", comment.ID), fmt.Sprintf(`{"blog_id":%d,"author_id":9999,"body":"Changed"}`, b.ID), 409)
	unchanged := read[model.Comment](t, c.request("GET", fmt.Sprintf("/comments/%d", comment.ID), "", 200))
	if unchanged.Body != "Hello" || unchanged.AuthorID != u.ID {
		t.Fatal("failed comment update changed data")
	}
	c.request("DELETE", fmt.Sprintf("/tags/%d", tag.ID), "", 204)
	got := read[model.Blog](t, c.request("GET", fmt.Sprintf("/blogs/%d", b.ID), "", 200))
	if len(got.Tags) != 0 {
		t.Fatal("deleted tag still assigned")
	}
	c.request("DELETE", fmt.Sprintf("/blogs/%d", b.ID), "", 204)
	c.request("GET", fmt.Sprintf("/comments/%d", comment.ID), "", 404)
	c.request("DELETE", fmt.Sprintf("/users/%d", u.ID), "", 204)
}

func TestBlogTagTransactions(t *testing.T) {
	t.Parallel()
	c := setup(t)
	u, tag, b := c.seed()
	badBody := fmt.Sprintf(`{"title":"Must roll back","body":"Changed","author_id":%d,"tag_ids":[%d,9999]}`, u.ID, tag.ID)
	c.request("POST", "/blogs", badBody, 409)
	c.request("PUT", fmt.Sprintf("/blogs/%d", b.ID), badBody, 409)
	blogs := read[[]model.Blog](t, c.request("GET", "/blogs", "", 200))
	if len(blogs) != 1 || blogs[0].Title != b.Title || blogs[0].Body != b.Body || len(blogs[0].Tags) != 1 || blogs[0].Tags[0].ID != tag.ID {
		t.Fatalf("partial transaction persisted: %+v", blogs)
	}
	second := read[model.Tag](t, c.request("POST", "/tags", `{"name":"SQLite"}`, 201))
	newBody := fmt.Sprintf(`{"title":"Retagged","body":"Hello","author_id":%d,"tag_ids":[%d]}`, u.ID, second.ID)
	updated := read[model.Blog](t, c.request("PUT", fmt.Sprintf("/blogs/%d", b.ID), newBody, 200))
	if len(updated.Tags) != 1 || updated.Tags[0].ID != second.ID {
		t.Fatal("tags not replaced")
	}
}

func TestListsAndFilters(t *testing.T) {
	t.Parallel()
	c := setup(t)
	for _, path := range []string{"/users", "/blogs", "/comments", "/tags"} {
		if got := strings.TrimSpace(c.request("GET", path, "", 200).Body.String()); got != "[]" {
			t.Fatalf("%s: empty list = %s", path, got)
		}
	}
	u, tag, b := c.seed()
	second := read[model.Blog](t, c.request("POST", "/blogs", fmt.Sprintf(`{"title":"Second","body":"Hello","author_id":%d}`, u.ID), 201))
	for _, blogID := range []int64{b.ID, second.ID} {
		c.request("POST", "/comments", fmt.Sprintf(`{"blog_id":%d,"author_id":%d,"body":"Hello"}`, blogID, u.ID), 201)
	}
	page := read[[]model.Blog](t, c.request("GET", "/blogs?limit=1&offset=1", "", 200))
	if len(page) != 1 || page[0].ID != second.ID {
		t.Fatal(page)
	}
	filtered := read[[]model.Blog](t, c.request("GET", fmt.Sprintf("/blogs?tag_id=%d", tag.ID), "", 200))
	if len(filtered) != 1 || filtered[0].ID != b.ID {
		t.Fatal(filtered)
	}
	comments := read[[]model.Comment](t, c.request("GET", fmt.Sprintf("/comments?blog_id=%d", second.ID), "", 200))
	if len(comments) != 1 || comments[0].BlogID != second.ID {
		t.Fatal(comments)
	}
	if got := strings.TrimSpace(c.request("GET", "/blogs?tag_id=9999", "", 200).Body.String()); got != "[]" {
		t.Fatal(got)
	}
	if got := strings.TrimSpace(c.request("GET", "/comments?blog_id=9999", "", 200).Body.String()); got != "[]" {
		t.Fatal(got)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	c := setup(t)
	cases := []struct{ name, path, body string }{
		{"empty user", "/users", `{}`},
		{"blank name", "/users", `{"name":"  ","email":"a@example.com"}`},
		{"bad email", "/users", `{"name":"Ada","email":"bad"}`},
		{"display name email", "/users", `{"name":"Ada","email":"Ada <ada@example.com>"}`},
		{"long name", "/users", fmt.Sprintf(`{"name":%q,"email":"a@example.com"}`, strings.Repeat("x", 101))},
		{"long email", "/users", fmt.Sprintf(`{"name":"Ada","email":%q}`, strings.Repeat("x", 250)+"@example.com")},
		{"blank tag", "/tags", `{"name":"  "}`},
		{"long tag", "/tags", fmt.Sprintf(`{"name":%q}`, strings.Repeat("x", 51))},
		{"empty blog", "/blogs", `{}`},
		{"missing body", "/blogs", `{"title":"Post","author_id":1}`},
		{"bad author", "/blogs", `{"title":"Post","body":"Text","author_id":0}`},
		{"long title", "/blogs", fmt.Sprintf(`{"title":%q,"body":"Text","author_id":1}`, strings.Repeat("x", 201))},
		{"long blog", "/blogs", fmt.Sprintf(`{"title":"Post","body":%q,"author_id":1}`, strings.Repeat("x", 100001))},
		{"duplicate tags", "/blogs", `{"title":"Post","body":"Text","author_id":1,"tag_ids":[1,1]}`},
		{"bad tag", "/blogs", `{"title":"Post","body":"Text","author_id":1,"tag_ids":[-1]}`},
		{"many tags", "/blogs", `{"title":"Post","body":"Text","author_id":1,"tag_ids":[` + strings.Repeat("1,", 100) + `1]}`},
		{"bad comment blog", "/comments", `{"blog_id":0,"author_id":1,"body":"Text"}`},
		{"bad comment author", "/comments", `{"blog_id":1,"author_id":0,"body":"Text"}`},
		{"blank comment", "/comments", `{"blog_id":1,"author_id":1,"body":"  "}`},
		{"long comment", "/comments", fmt.Sprintf(`{"blog_id":1,"author_id":1,"body":%q}`, strings.Repeat("x", 10001))},
		{"unknown field", "/tags", `{"name":"Go","admin":true}`},
		{"read only id", "/tags", `{"id":1,"name":"Go"}`},
		{"wrong type", "/users", `{"name":1,"email":"a@example.com"}`},
		{"malformed", "/tags", `{"name":`},
		{"multiple objects", "/tags", `{"name":"Go"} {"name":"Other"}`},
		{"trailing garbage", "/tags", `{"name":"Go"} trailing`},
		{"null", "/tags", `null`},
		{"array", "/tags", `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { c.request("POST", tc.path, tc.body, 400) })
	}
	for _, path := range []string{"/users?limit=0", "/users?limit=101", "/users?limit=", "/users?offset=-1", "/users?offset=1000001", "/users?limit=abc", "/blogs?tag_id=0", "/comments?blog_id=nope", "/users/0", "/users/-1", "/users/nope", "/users/999999999999999999999999"} {
		t.Run(path, func(t *testing.T) { c.request("GET", path, "", 400) })
	}
	c.request("PUT", "/tags/nope", `{"name":"Go"}`, 400)
	c.request("DELETE", "/tags/nope", "", 400)
}

func TestHTTPResponses(t *testing.T) {
	t.Parallel()
	c := setup(t)
	c.request("GET", "/health", "", 200)
	c.request("GET", "/does-not-exist", "", 404)
	for _, resource := range []string{"users", "blogs", "comments", "tags"} {
		c.request("GET", "/"+resource+"/9999", "", 404)
		c.request("DELETE", "/"+resource+"/9999", "", 404)
	}
	c.request("PUT", "/users/9999", `{"name":"Ada","email":"ada@example.com"}`, 404)
	c.request("PUT", "/blogs/9999", `{"title":"Post","body":"Text","author_id":1}`, 404)
	c.request("PUT", "/comments/9999", `{"blog_id":1,"author_id":1,"body":"Text"}`, 404)
	c.request("PUT", "/tags/9999", `{"name":"Go"}`, 404)
	w := c.request("PATCH", "/tags/1", `{"name":"Go"}`, 405)
	if !strings.Contains(w.Header().Get("Allow"), "PUT") {
		t.Fatal("missing Allow header")
	}
	for _, contentType := range []string{"", "text/plain", "application/jsonish"} {
		r := httptest.NewRequest("POST", "/tags", strings.NewReader(`{"name":"Go"}`))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		c.h.ServeHTTP(w, r)
		if w.Code != 415 {
			t.Fatalf("content type %q: %d", contentType, w.Code)
		}
	}
	c.request("POST", "/tags", " ", 400)
	c.request("POST", "/tags", `{"name":"`+strings.Repeat("x", 1<<20)+`"}`, 413)
	c.request("POST", "/tags", `{"name":"Go"}`+strings.Repeat(" ", 1<<20), 413)
	r := httptest.NewRequest("POST", "/tags", strings.NewReader(`{"name":"Go"}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	w = httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("JSON charset: %d", w.Code)
	}
}

func TestDatabaseFailure(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	h := api.New(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, h: h}
	c.request("GET", "/health", "", 503)
	w := c.request("GET", "/users", "", 500)
	if strings.Contains(w.Body.String(), "sql") {
		t.Fatal("database details leaked")
	}
}
