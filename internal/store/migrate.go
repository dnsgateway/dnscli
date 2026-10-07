package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// migrations are applied in order; the schema version is stored in PRAGMA user_version.
// Never edit an applied migration: append a new one.
var migrations = []string{
	// 1: Accounts, Zones and Records.
	`
	CREATE TABLE accounts (
		id       INTEGER PRIMARY KEY,
		provider TEXT NOT NULL,
		name     TEXT NOT NULL,
		UNIQUE (provider, name)
	);

	CREATE TABLE zones (
		id               INTEGER PRIMARY KEY,
		account_id       INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		name             TEXT NOT NULL,
		provider_zone_id TEXT NOT NULL DEFAULT '',
		soa_serial       INTEGER NOT NULL DEFAULT 0,
		refreshed_at     INTEGER NOT NULL DEFAULT 0, -- unix seconds, 0 = never
		UNIQUE (account_id, name)
	);

	CREATE TABLE records (
		id                 INTEGER PRIMARY KEY,
		zone_id            INTEGER NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
		provider_record_id TEXT NOT NULL DEFAULT '',
		type               TEXT NOT NULL,
		name               TEXT NOT NULL,
		value              TEXT NOT NULL,
		ttl                INTEGER NOT NULL,
		extras             TEXT NOT NULL DEFAULT '{}' -- JSON object
	);

	CREATE INDEX records_zone ON records(zone_id);
	`,
}

// migrate applies pending migrations in one write transaction. BEGIN IMMEDIATE
// takes the write lock before the schema version is read, so two processes
// opening a fresh database cannot both apply the same migration.
func migrate(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	if err := migrateLocked(ctx, conn); err != nil {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")

		return err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	if err := enableWAL(ctx, conn); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}

	return nil
}

// enableWAL switches the database to WAL. The switch needs an exclusive lock
// and SQLite does not run the busy handler for it, so retry while another
// connection holds a lock, for as long as busy_timeout would wait.
func enableWAL(ctx context.Context, conn *sql.Conn) error {
	deadline := time.Now().Add(busyTimeout)

	for {
		_, err := conn.ExecContext(ctx, "PRAGMA journal_mode = WAL")

		var se *sqlite.Error
		if err == nil || !errors.As(err, &se) || se.Code()&0xff != sqlite3.SQLITE_BUSY || time.Now().After(deadline) {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func migrateLocked(ctx context.Context, conn *sql.Conn) error {
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this dnscli supports (%d)", version, len(migrations))
	}

	for i := version; i < len(migrations); i++ {
		if _, err := conn.ExecContext(ctx, migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}

		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}

	return nil
}
