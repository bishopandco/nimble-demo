// Package store owns SQLite persistence and schema migrations.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	"modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrConflict = errors.New("duplicate value or conflicting resource reference")
)

//go:embed schema.sql
var schema string

type Store struct{ db *sql.DB }

type ListOptions struct {
	Limit     int
	Offset    int
	RelatedID int64
}

// Open creates the database if needed and applies the initial migration atomically.
// A single connection serializes writes and keeps reads of :memory: consistent.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file::memory:?"
	if path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		dsn = (&url.URL{Scheme: "file", Path: absolute}).String() + "?"
	}
	// DSN pragmas are applied to every connection, including replacement connections.
	dsn += "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) migrate(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Lock before checking the version so concurrent startups cannot both migrate.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("unsupported schema version %d", version)
	}
	if version == 0 {
		if _, err := conn.ExecContext(ctx, schema); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

func databaseError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 19 { // SQLITE_CONSTRAINT
		return ErrConflict
	}
	return err
}

func changed(result sql.Result, err error) error {
	if err != nil {
		return databaseError(err)
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return ErrNotFound
	}
	return err
}
