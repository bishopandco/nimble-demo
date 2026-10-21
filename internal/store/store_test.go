package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go-api/internal/model"
)

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func TestPersistenceAndMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// These characters must be treated as filename characters, not DSN parameters.
	path := filepath.Join(t.TempDir(), "blog ?#.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	u, err := first.CreateUser(ctx, model.UserInput{Name: "Ada", Email: "ada@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := first.CreateTag(ctx, model.TagInput{Name: "Go"})
	if err != nil {
		t.Fatal(err)
	}
	blog, err := first.CreateBlog(ctx, model.BlogInput{Title: "Post", Body: "Text", AuthorID: u.ID, TagIDs: []int64{tag.ID}})
	if err != nil {
		t.Fatal(err)
	}
	comment, err := first.CreateComment(ctx, model.CommentInput{BlogID: blog.ID, AuthorID: u.ID, Body: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := openTestStore(t, path)
	gotUser, err := second.GetUser(ctx, u.ID)
	if err != nil || gotUser != u {
		t.Fatalf("user did not persist: %+v, %v", gotUser, err)
	}
	gotBlog, err := second.GetBlog(ctx, blog.ID)
	if err != nil || gotBlog.Title != blog.Title || len(gotBlog.Tags) != 1 || gotBlog.Tags[0] != tag {
		t.Fatalf("blog did not persist: %+v, %v", gotBlog, err)
	}
	gotComment, err := second.GetComment(ctx, comment.ID)
	if err != nil || gotComment != comment {
		t.Fatalf("comment did not persist: %+v, %v", gotComment, err)
	}
	var version, foreignKeys int
	if err := second.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := second.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if version != 1 || foreignKeys != 1 {
		t.Fatalf("version=%d, foreign_keys=%d", version, foreignKeys)
	}
	var integrity string
	if err := second.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q, error=%v", integrity, err)
	}
}

func TestRejectFutureSchema(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "future.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	_, err = Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "unsupported schema version 2") {
		t.Fatalf("unexpected migration error: %v", err)
	}
	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatal("future schema was modified")
	}
}

func TestMigrationRollback(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "conflict.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// Force the migration to fail after it has created other tables.
	if _, err := raw.Exec("CREATE TABLE tags (existing TEXT)"); err != nil {
		t.Fatal(err)
	}
	if db, err := Open(context.Background(), path); err == nil {
		db.Close()
		t.Fatal("expected migration failure")
	}
	var count, version int
	if err := raw.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('users', 'blogs', 'comments')").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if count != 0 || version != 0 {
		t.Fatalf("partial migration persisted: tables=%d version=%d", count, version)
	}
}

func TestForeignKeysSurviveConnectionReplacement(t *testing.T) {
	t.Parallel()
	db := openTestStore(t, filepath.Join(t.TempDir(), "replace.db"))
	db.db.SetMaxIdleConns(0)
	for range 3 {
		_, err := db.CreateBlog(context.Background(), model.BlogInput{Title: "Post", Body: "Text", AuthorID: 9999})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("foreign key not enforced: %v", err)
		}
	}
}

func TestCommentAuthorDeletionRestricted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestStore(t, ":memory:")
	author, err := db.CreateUser(ctx, model.UserInput{Name: "Author", Email: "author@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := db.CreateUser(ctx, model.UserInput{Name: "Reader", Email: "reader@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	blog, err := db.CreateBlog(ctx, model.BlogInput{Title: "Post", Body: "Text", AuthorID: author.ID})
	if err != nil {
		t.Fatal(err)
	}
	comment, err := db.CreateComment(ctx, model.CommentInput{BlogID: blog.ID, AuthorID: reader.ID, Body: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(ctx, reader.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("comment author deleted: %v", err)
	}
	if err := db.DeleteComment(ctx, comment.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(ctx, reader.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentBlogWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestStore(t, filepath.Join(t.TempDir(), "concurrent.db"))
	u, err := db.CreateUser(ctx, model.UserInput{Name: "Ada", Email: "ada@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := db.CreateTag(ctx, model.TagInput{Name: "Go"})
	if err != nil {
		t.Fatal(err)
	}
	const count = 12
	results := make(chan error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := db.CreateBlog(ctx, model.BlogInput{Title: fmt.Sprintf("Post %d", i), Body: "Text", AuthorID: u.ID, TagIDs: []int64{tag.ID}})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
	blogs, err := db.ListBlogs(ctx, ListOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(blogs) != count {
		t.Fatalf("got %d blogs, want %d", len(blogs), count)
	}
	for _, blog := range blogs {
		if len(blog.Tags) != 1 || blog.Tags[0].ID != tag.ID {
			t.Fatalf("missing tag after concurrent write: %+v", blog)
		}
	}
}

func TestCanceledOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Open(ctx, ":memory:")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
