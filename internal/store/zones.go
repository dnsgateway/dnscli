package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// PutZone inserts a Zone, or updates the existing Zone with the same
// Account and zone name, and returns it as stored, with its ID set.
// A zero Serial or RefreshedAt, or no Nameservers, keeps the cached value,
// so relisting an Account's Zones does not forget their last Refresh. The
// Zone Status is never written here; it keeps the cached one. A changed
// Provider zone ID is another zone at the Provider, so it forgets the
// cached Zone Status and serial, which belonged to the old one.
func (s *Store) PutZone(ctx context.Context, z dns.Zone) (dns.Zone, error) {
	return putZone(ctx, s.db, z)
}

// queryRower is a database handle or connection that runs one-row queries.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func putZone(ctx context.Context, db queryRower, z dns.Zone) (dns.Zone, error) {
	var (
		refreshed, statusAt int64
		ns                  string
	)

	err := db.QueryRowContext(ctx, `
		INSERT INTO zones (account_id, name, provider_zone_id, soa_serial, refreshed_at, nameservers)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id, name) DO UPDATE SET
			provider_zone_id = excluded.provider_zone_id,
			soa_serial       = IIF(excluded.soa_serial = 0 AND excluded.provider_zone_id = provider_zone_id, soa_serial, excluded.soa_serial),
			refreshed_at     = IIF(excluded.refreshed_at = 0, refreshed_at, excluded.refreshed_at),
			nameservers      = IIF(excluded.nameservers = '', nameservers, excluded.nameservers),
			dnssec           = IIF(excluded.provider_zone_id = provider_zone_id, dnssec, 0),
			hsts             = IIF(excluded.provider_zone_id = provider_zone_id, hsts, 0),
			status_at        = IIF(excluded.provider_zone_id = provider_zone_id, status_at, 0)
		RETURNING id, soa_serial, refreshed_at, dnssec, hsts, status_at, nameservers`,
		z.AccountID, z.Name, z.ProviderID, z.Serial, unixOrZero(z.RefreshedAt), joinNames(z.Nameservers),
	).Scan(&z.ID, &z.Serial, &refreshed, &z.Status.DNSSEC, &z.Status.HSTS, &statusAt, &ns)
	if err != nil {
		return dns.Zone{}, fmt.Errorf("put zone %s: %w", z.Name, err)
	}

	z.RefreshedAt = timeOrZero(refreshed)
	z.StatusAt = timeOrZero(statusAt)
	z.Nameservers = splitNames(ns)

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

			// The schema cascades the delete to the Zone's Records.
			if _, err := conn.ExecContext(ctx, `DELETE FROM zones WHERE id = ?`, z.ID); err != nil {
				return err
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
		SELECT z.id, z.account_id, z.name, z.provider_zone_id, z.soa_serial, z.refreshed_at,
			z.dnssec, z.hsts, z.status_at, z.nameservers
		FROM zones z JOIN accounts a ON a.id = z.account_id
		ORDER BY z.name, a.provider, a.name`)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	defer rows.Close()

	var out []dns.Zone

	for rows.Next() {
		var (
			z                   dns.Zone
			refreshed, statusAt int64
			ns                  string
		)

		if err := rows.Scan(&z.ID, &z.AccountID, &z.Name, &z.ProviderID, &z.Serial, &refreshed,
			&z.Status.DNSSEC, &z.Status.HSTS, &statusAt, &ns); err != nil {
			return nil, fmt.Errorf("list zones: %w", err)
		}

		z.RefreshedAt = timeOrZero(refreshed)
		z.StatusAt = timeOrZero(statusAt)
		z.Nameservers = splitNames(ns)

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
// the cached serial, as PutZone does. It also stores z's Zone Status when
// z.StatusAt is set, and keeps the cached one when it is zero, such as
// when the Refresh could not read it. It returns the Zone as stored.
//
// It returns ErrZoneNotCached unless the cache still has that very Zone:
// the same ID, Account, zone name and Provider zone ID. A Zone removed
// while its Refresh was in flight may have handed its ID to a new one.
func (s *Store) SaveRefresh(ctx context.Context, z dns.Zone, records []dns.Record, serial uint32, at time.Time) (dns.Zone, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dns.Zone{}, fmt.Errorf("save refresh: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var refreshed, statusAt int64

	err = tx.QueryRowContext(ctx, `
		UPDATE zones SET
			soa_serial   = IIF(?1 = 0, soa_serial, ?1),
			refreshed_at = ?2,
			dnssec       = IIF(?7 = 0, dnssec, ?8),
			hsts         = IIF(?7 = 0, hsts, ?9),
			status_at    = IIF(?7 = 0, status_at, ?7)
		WHERE id = ?3 AND account_id = ?4 AND name = ?5 AND provider_zone_id = ?6
		RETURNING soa_serial, refreshed_at, dnssec, hsts, status_at`,
		serial, unixOrZero(at), z.ID, z.AccountID, z.Name, z.ProviderID,
		unixOrZero(z.StatusAt), z.Status.DNSSEC, z.Status.HSTS,
	).Scan(&z.Serial, &refreshed, &z.Status.DNSSEC, &z.Status.HSTS, &statusAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrZoneNotCached
	}

	if err != nil {
		return dns.Zone{}, fmt.Errorf("save refresh: %w", err)
	}

	z.RefreshedAt = timeOrZero(refreshed)
	z.StatusAt = timeOrZero(statusAt)

	if err := replaceRecords(ctx, tx, z.ID, records); err != nil {
		return dns.Zone{}, fmt.Errorf("save refresh: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return dns.Zone{}, fmt.Errorf("save refresh: %w", err)
	}

	return z, nil
}

// SaveApply updates a Zone's cached Records with the changes its Provider
// confirmed applying, in one transaction. Each change's After is the
// Record as the Provider stored it: an addition inserts it, a modification
// replaces the cached Record with its Before's Provider record ID, and a
// deletion removes that Record. A modification whose Record is no longer
// cached inserts it. The rest of the cache is left alone.
//
// Like SaveRefresh, it returns ErrZoneNotCached unless the cache still has
// that very Zone: the same ID, Account, zone name and Provider zone ID.
func (s *Store) SaveApply(ctx context.Context, z dns.Zone, applied []dns.Change) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save apply: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var found bool

	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM zones WHERE id = ? AND account_id = ? AND name = ? AND provider_zone_id = ?)`,
		z.ID, z.AccountID, z.Name, z.ProviderID,
	).Scan(&found)
	if err == nil && !found {
		err = ErrZoneNotCached
	}

	if err != nil {
		return fmt.Errorf("save apply: %w", err)
	}

	for _, c := range applied {
		if c.Kind != dns.ChangeAdd {
			if _, err := tx.ExecContext(ctx, `DELETE FROM records WHERE zone_id = ? AND provider_record_id = ?`,
				z.ID, c.Before.ProviderID); err != nil {
				return fmt.Errorf("save apply: %w", err)
			}
		}

		if c.Kind == dns.ChangeDelete {
			continue
		}

		if err := insertRecord(ctx, tx, z.ID, c.After); err != nil {
			return fmt.Errorf("save apply: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save apply: %w", err)
	}

	return nil
}

// replaceRecords replaces a Zone's cached Records inside tx.
func replaceRecords(ctx context.Context, tx *sql.Tx, zoneID int64, records []dns.Record) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM records WHERE zone_id = ?`, zoneID); err != nil {
		return err
	}

	for _, r := range records {
		if err := insertRecord(ctx, tx, zoneID, r); err != nil {
			return err
		}
	}

	return nil
}

// insertRecord caches one Record of a Zone inside tx.
func insertRecord(ctx context.Context, tx *sql.Tx, zoneID int64, r dns.Record) error {
	extras, err := json.Marshal(nonNil(r.Extras))
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO records (zone_id, provider_record_id, type, name, value, ttl, extras)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		zoneID, r.ProviderID, r.Type, r.Name, r.Value, r.TTL, string(extras),
	)

	return err
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

// joinNames stores a list of DNS names, such as nameservers, in one column.
// DNS names hold no spaces.
func joinNames(names []string) string {
	return strings.Join(names, " ")
}

// splitNames reads a list stored by joinNames; nil when empty.
func splitNames(s string) []string {
	if s == "" {
		return nil
	}

	return strings.Fields(s)
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}

	return m
}
