package ecs

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteLog persists the event log in one table. Components are not
// stored — they are rebuilt from this log at boot (invariant 1).
type SQLiteLog struct {
	db *sql.DB
}

// OpenSQLite opens (creating if needed) the log at path.
func OpenSQLite(path string) (*SQLiteLog, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS events (
		seq     INTEGER PRIMARY KEY AUTOINCREMENT,
		at      TEXT NOT NULL,
		system  TEXT NOT NULL,
		type    TEXT NOT NULL,
		entity  TEXT,
		payload BLOB
	)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("ecs: init schema: %w", err)
	}
	return &SQLiteLog{db: db}, nil
}

func (l *SQLiteLog) Close() error { return l.db.Close() }

func (l *SQLiteLog) Append(ev Event) (int64, error) {
	var entity any
	if ev.Entity != "" {
		entity = string(ev.Entity)
	}
	var payload any
	if len(ev.Payload) > 0 {
		payload = []byte(ev.Payload)
	}
	var seq int64
	err := l.db.QueryRow(
		`INSERT INTO events (at, system, type, entity, payload) VALUES (?, ?, ?, ?, ?) RETURNING seq`,
		ev.At.UTC().Format(time.RFC3339Nano), ev.System, ev.Type, entity, payload,
	).Scan(&seq)
	return seq, err
}

func (l *SQLiteLog) Replay(fn func(Event) error) error {
	rows, err := l.db.Query(`SELECT seq, at, system, type, entity, payload FROM events ORDER BY seq`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			ev      Event
			at      string
			entity  sql.NullString
			payload []byte
		)
		if err := rows.Scan(&ev.Seq, &at, &ev.System, &ev.Type, &entity, &payload); err != nil {
			return err
		}
		if ev.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return fmt.Errorf("ecs: seq %d: bad timestamp %q", ev.Seq, at)
		}
		ev.Entity = EntityID(entity.String)
		ev.Payload = payload
		if err := fn(ev); err != nil {
			return err
		}
	}
	return rows.Err()
}
