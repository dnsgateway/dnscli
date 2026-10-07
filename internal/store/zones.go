package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// PutZone inserts a Zone, or updates the existing Zone with the same
// Account and zone name, and returns it with its ID set.
func (s *Store) PutZone(ctx context.Context, z dns.Zone) (dns.Zone, error) {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO zones (account_id, name, provider_zone_id, soa_serial, refreshed_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (account_id, name) DO UPDATE SET
			provider_zone_id = excluded.provider_zone_id,
			soa_serial       = excluded.soa_serial,
			refreshed_at     = excluded.refreshed_at
		RETURNING id`,
		z.AccountID, z.Name, z.ProviderID, z.Serial, unixOrZero(z.RefreshedAt),
	).Scan(&z.ID)
	if err != nil {
		return dns.Zone{}, fmt.Errorf("put zone %s: %w", z.Name, err)
	}

	return z, nil
}

// Zones returns every cached Zone, ordered by zone name and then Account.
func (s *Store) Zones(ctx context.Context) ([]dns.Zone, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT z.id, z.account_id, z.name, z.provider_zone_id, z.soa_serial, z.refreshed_at
		FROM zones z JOIN accounts a ON a.id = z.account_id
		ORDER BY z.name, a.provider, a.name`)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	defer rows.Close()

	var out []dns.Zone

	for rows.Next() {
		var (
			z         dns.Zone
			refreshed int64
		)

		if err := rows.Scan(&z.ID, &z.AccountID, &z.Name, &z.ProviderID, &z.Serial, &refreshed); err != nil {
			return nil, fmt.Errorf("list zones: %w", err)
		}

		if refreshed != 0 {
			z.RefreshedAt = time.Unix(refreshed, 0).UTC()
		}

		out = append(out, z)
	}

	return out, rows.Err()
}

// ReplaceRecords replaces a Zone's cached Records in one transaction.
func (s *Store) ReplaceRecords(ctx context.Context, zoneID int64, records []dns.Record) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace records: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `DELETE FROM records WHERE zone_id = ?`, zoneID); err != nil {
		return fmt.Errorf("replace records: %w", err)
	}

	for _, r := range records {
		extras, err := json.Marshal(nonNil(r.Extras))
		if err != nil {
			return fmt.Errorf("replace records: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO records (zone_id, provider_record_id, type, name, value, ttl, extras)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			zoneID, r.ProviderID, r.Type, r.Name, r.Value, r.TTL, string(extras),
		); err != nil {
			return fmt.Errorf("replace records: %w", err)
		}
	}

	return tx.Commit()
}

// Records returns a Zone's cached Records, ordered by name and type.
func (s *Store) Records(ctx context.Context, zoneID int64) ([]dns.Record, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider_record_id, type, name, value, ttl, extras
		FROM records WHERE zone_id = ?
		ORDER BY name, type, value`, zoneID)
	if err != nil {
		return nil, fmt.Errorf("list records: %w", err)
	}
	defer rows.Close()

	var out []dns.Record

	for rows.Next() {
		var (
			r      dns.Record
			extras string
		)

		if err := rows.Scan(&r.ProviderID, &r.Type, &r.Name, &r.Value, &r.TTL, &extras); err != nil {
			return nil, fmt.Errorf("list records: %w", err)
		}

		if err := json.Unmarshal([]byte(extras), &r.Extras); err != nil {
			return nil, fmt.Errorf("list records: extras of %s %s: %w", r.Type, r.Name, err)
		}

		if len(r.Extras) == 0 {
			r.Extras = nil
		}

		out = append(out, r)
	}

	return out, rows.Err()
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.Unix()
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}

	return m
}
