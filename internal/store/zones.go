package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// PutZone inserts a Zone, or updates the existing Zone with the same
// Account and zone name, and returns it as stored, with its ID set.
// A zero Serial or RefreshedAt keeps the cached value, so relisting an
// Account's Zones does not forget their last Refresh.
func (s *Store) PutZone(ctx context.Context, z dns.Zone) (dns.Zone, error) {
	return putZone(ctx, s.db, z)
}

// queryRower is a database handle or connection that runs one-row queries.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func putZone(ctx context.Context, db queryRower, z dns.Zone) (dns.Zone, error) {
	var refreshed int64

	err := db.QueryRowContext(ctx, `
		INSERT INTO zones (account_id, name, provider_zone_id, soa_serial, refreshed_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (account_id, name) DO UPDATE SET
			provider_zone_id = excluded.provider_zone_id,
			soa_serial       = IIF(excluded.soa_serial = 0, soa_serial, excluded.soa_serial),
			refreshed_at     = IIF(excluded.refreshed_at = 0, refreshed_at, excluded.refreshed_at)
		RETURNING id, soa_serial, refreshed_at`,
		z.AccountID, z.Name, z.ProviderID, z.Serial, unixOrZero(z.RefreshedAt),
	).Scan(&z.ID, &z.Serial, &refreshed)
	if err != nil {
		return dns.Zone{}, fmt.Errorf("put zone %s: %w", z.Name, err)
	}

	z.RefreshedAt = timeOrZero(refreshed)

	return z, nil
}

// ZoneSync is what SyncAccountZones changed.
type ZoneSync struct {
	// Added are the Zones that were not cached before.
	Added []dns.Zone
	// Dropped are the cached Zones the list no longer had; their Records
	// were deleted with them.
	Dropped []dns.Zone
}

// SyncAccountZones makes an Account's cached Zones match a Zone list from
// its Provider, in one transaction. Each listed Zone is stored as PutZone
// stores it, so a Zone that was already cached keeps its ID, serial, last
// Refresh time and Records. A cached Zone the list does not have is
// deleted with its Records. It returns ErrAccountNotFound when the Account
// was removed meanwhile.
func (s *Store) SyncAccountZones(ctx context.Context, accountID int64, zones []dns.Zone) (ZoneSync, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return ZoneSync{}, fmt.Errorf("sync zones: %w", err)
	}
	defer conn.Close()

	var res ZoneSync

	err = writeLocked(ctx, conn, func() error {
		var found bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id = ?)`, accountID).Scan(&found); err != nil {
			return err
		}

		if !found {
			return ErrAccountNotFound
		}

		cached, err := accountZones(ctx, conn, accountID)
		if err != nil {
			return err
		}

		listed := map[string]bool{}

		for _, z := range zones {
			z.AccountID = accountID
			listed[z.Name] = true

			stored, err := putZone(ctx, conn, z)
			if err != nil {
				return err
			}

			if _, ok := cached[z.Name]; !ok {
				res.Added = append(res.Added, stored)
			}
		}

		for name, z := range cached {
			if listed[name] {
				continue
			}

			for _, q := range []string{`DELETE FROM records WHERE zone_id = ?`, `DELETE FROM zones WHERE id = ?`} {
				if _, err := conn.ExecContext(ctx, q, z.ID); err != nil {
					return err
				}
			}

			res.Dropped = append(res.Dropped, z)
		}

		return nil
	})
	if err != nil {
		return ZoneSync{}, fmt.Errorf("sync zones: %w", err)
	}

	return res, nil
}

// accountZones returns an Account's cached Zones by zone name.
func accountZones(ctx context.Context, conn *sql.Conn, accountID int64) (map[string]dns.Zone, error) {
	rows, err := conn.QueryContext(ctx, `SELECT id, name, provider_zone_id FROM zones WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]dns.Zone{}

	for rows.Next() {
		z := dns.Zone{AccountID: accountID}
		if err := rows.Scan(&z.ID, &z.Name, &z.ProviderID); err != nil {
			return nil, err
		}

		out[z.Name] = z
	}

	return out, rows.Err()
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

		z.RefreshedAt = timeOrZero(refreshed)

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

	if err := replaceRecords(ctx, tx, zoneID, records); err != nil {
		return fmt.Errorf("replace records: %w", err)
	}

	return tx.Commit()
}

// ErrZoneNotCached means the Zone is no longer in the cache, for example
// because its Account was removed.
var ErrZoneNotCached = errors.New("the Zone is no longer cached")

// SaveRefresh stores the result of a Refresh in one transaction: it
// replaces the Zone's cached Records and sets its SOA serial and its last
// Refresh time. A zero serial, when the Provider did not report one, keeps
// the cached serial, as PutZone does.
func (s *Store) SaveRefresh(ctx context.Context, zoneID int64, records []dns.Record, serial uint32, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save refresh: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.ExecContext(ctx, `UPDATE zones SET soa_serial = IIF(?1 = 0, soa_serial, ?1), refreshed_at = ?2 WHERE id = ?3`,
		serial, unixOrZero(at), zoneID)
	if err != nil {
		return fmt.Errorf("save refresh: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("save refresh: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("save refresh: %w", ErrZoneNotCached)
	}

	if err := replaceRecords(ctx, tx, zoneID, records); err != nil {
		return fmt.Errorf("save refresh: %w", err)
	}

	return tx.Commit()
}

// replaceRecords replaces a Zone's cached Records inside tx.
func replaceRecords(ctx context.Context, tx *sql.Tx, zoneID int64, records []dns.Record) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM records WHERE zone_id = ?`, zoneID); err != nil {
		return err
	}

	for _, r := range records {
		extras, err := json.Marshal(nonNil(r.Extras))
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO records (zone_id, provider_record_id, type, name, value, ttl, extras)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			zoneID, r.ProviderID, r.Type, r.Name, r.Value, r.TTL, string(extras),
		); err != nil {
			return err
		}
	}

	return nil
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

func timeOrZero(unix int64) time.Time {
	if unix == 0 {
		return time.Time{}
	}

	return time.Unix(unix, 0).UTC()
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}

	return m
}
