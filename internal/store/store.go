// Package store is dnscli's single local SQLite database.
//
// It holds Account definitions with their encrypted secrets, the Unseal
// Method key slots, and the cached Zones and Records. It is not a
// disposable cache: losing it loses Account definitions.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// busyTimeout is how long a connection waits for another one's lock.
const busyTimeout = 5 * time.Second

// Store is the local SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	// Escape the path so '#', '?' and '%' in it are not read as URI syntax.
	// WAL is enabled by migrate: switching to it on connect can fail with
	// SQLITE_BUSY while another process creates the database.
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() +
		fmt.Sprintf("?_pragma=foreign_keys(1)&_pragma=busy_timeout(%d)", busyTimeout.Milliseconds())

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// One connection keeps pragmas and transactions simple for an embedded database.
	db.SetMaxOpenConns(1)

	if err := migrate(ctx, db); err != nil {
		_ = db.Close()

		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}
