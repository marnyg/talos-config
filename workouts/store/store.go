// Package store is the workouts tracker's persistence: one SQLite file,
// the schema in schema.sql, and typed queries over it. The domain is
// described in workouts/docs/desired-state/domain-model.md; the types
// here mirror it one-to-one. Everything derived (records, PRs, the
// "previous set") lives in records.go as queries, never as tables.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Store wraps the database. One Store per process; SQLite serialises
// writers itself and WAL mode lets readers proceed meanwhile.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies
// the schema. ":memory:" gives a throwaway store for tests.
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		// One connection, or each pool connection sees its own empty memory db.
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for ad-hoc reads (tests, admin).
func (s *Store) DB() *sql.DB { return s.db }

// ErrNotFound is returned by Get* for an unknown id.
var ErrNotFound = errors.New("not found")

// NewID mints a v4 UUID; imported rows keep their upstream ids.
func NewID() string { return uuid.NewString() }

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// Time formats t for storage (RFC 3339, UTC, nanoseconds).
func Time(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ParseTime is the inverse of Time; tolerant of the second-precision form.
func ParseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func jsonList(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func fromJSONList(s string) []string {
	var v []string
	_ = json.Unmarshal([]byte(s), &v)
	return v
}

// tx runs fn in a transaction.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Setting reads a setting, "" if unset.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting writes a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO setting(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
