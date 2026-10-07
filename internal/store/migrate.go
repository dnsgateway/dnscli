package store

import (
	"context"
	"database/sql"
	"fmt"
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

func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this dnscli supports (%d)", version, len(migrations))
	}

	for i := version; i < len(migrations); i++ {
		if err := apply(ctx, db, i+1, migrations[i]); err != nil {
			return err
		}
	}

	return nil
}

func apply(ctx context.Context, db *sql.DB, version int, stmt string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %d: %w", version, err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("migration %d: %w", version, err)
	}

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return fmt.Errorf("migration %d: %w", version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %d: %w", version, err)
	}

	return nil
}
