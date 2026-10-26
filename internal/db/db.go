// Package db persists Air-Mixer's configuration (servers, clients and
// routes) in SQLite. It uses a pure-Go SQLite driver, so the binary builds
// without cgo.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when creating a record whose ID already exists.
var ErrConflict = errors.New("already exists")

// DB is a handle to the configuration database. It is safe for concurrent
// use.
type DB struct {
	conn *sql.DB
}

// Open opens (creating if needed) the database at path and brings its schema
// up to date. Use ":memory:" for a private in-memory database.
func Open(ctx context.Context, path string) (*DB, error) {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	if path != ":memory:" {
		q.Add("_pragma", "journal_mode(WAL)")
	}
	conn, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// SQLite allows one writer at a time; a single connection avoids
	// "database is locked" errors and keeps :memory: databases shared.
	conn.SetMaxOpenConns(1)
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	db := &DB{conn: conn}
	if err := db.migrate(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the database.
func (db *DB) Close() error { return db.conn.Close() }

// Ping checks that the database is reachable.
func (db *DB) Ping(ctx context.Context) error { return db.conn.PingContext(ctx) }

// affectedOne converts a zero-row UPDATE or DELETE into ErrNotFound.
func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// isConstraint reports whether err is a uniqueness violation.
func isConstraint(err error) bool {
	type coder interface{ Code() int }
	var c coder
	// SQLITE_CONSTRAINT_PRIMARYKEY (1555) and SQLITE_CONSTRAINT_UNIQUE (2067).
	return errors.As(err, &c) && (c.Code() == 1555 || c.Code() == 2067)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
