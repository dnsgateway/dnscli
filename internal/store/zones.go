package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// PutZone inserts a Zone, or updates the existing Zone with the same
// Account and zone name, and returns it as stored, with its ID set.
// A zero Serial or RefreshedAt, or no Nameservers, keeps the cached value,
// so relisting an Account's Zones does not forget their last Refresh. So
// does an Unknown Hosting State: PutZone sets a Zone's state, it does not
// clear it. Only a listing, through SyncAccountZones, stores Unknown, which
// is what a Provider may list. The Zone Status is never written here; it
// keeps the cached one.
//
// A changed Provider zone ID is another zone at the Provider, so it
// forgets, in the same transaction, everything cached of the old one: its
// Records, serial, last Refresh time, nameservers, Zone Status and when it
// was first seen signed. Only what z itself sets is stored. replaced
// reports that it did so.
func (s *Store) PutZone(ctx context.Context, z dns.Zone) (stored dns.Zone, replaced bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dns.Zone{}, false, fmt.Errorf("put zone %s: %w", z.Name, err)
	}
	defer tx.Rollback() //nolint:errcheck

	z, replaced, err = putZone(ctx, tx, z, false)
	if err != nil {
		return dns.Zone{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return dns.Zone{}, false, fmt.Errorf("put zone %s: %w", z.Name, err)
	}

	return z, replaced, nil
}

// execQueryRower is a transaction, or a connection inside one, that runs
// statements and one-row queries.
type execQueryRower interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// putZone stores z as PutZone does, inside a transaction. A listing
// stores z's Hosting State even when it is Unknown. replaced reports that
// a cached Zone of that name had another Provider zone ID.
func putZone(ctx context.Context, db execQueryRower, z dns.Zone, listing bool) (stored dns.Zone, replaced bool, err error) {
	var oldID string

	scan := zoneScan{z: &z}

	err = db.QueryRowContext(ctx, `SELECT provider_zone_id FROM zones WHERE account_id = ? AND name = ?`,
		z.AccountID, z.Name).Scan(&oldID)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		err = nil
	case err == nil && oldID != z.ProviderID:
		replaced = true
		// The old zone's Records are not the new one's.
		_, err = db.ExecContext(ctx, `
			DELETE FROM records WHERE zone_id = (SELECT id FROM zones WHERE account_id = ? AND name = ?)`,
			z.AccountID, z.Name)
	}

	if err != nil {
		return dns.Zone{}, false, fmt.Errorf("put zone %s: %w", z.Name, err)
	}

	err = db.QueryRowContext(ctx, `
		INSERT INTO zones (account_id, name, provider_zone_id, soa_serial, refreshed_at, nameservers, hosting_state)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)
		ON CONFLICT (account_id, name) DO UPDATE SET
			provider_zone_id = excluded.provider_zone_id,
			hosting_state    = IIF(?8 = 0 AND excluded.hosting_state = '' AND excluded.provider_zone_id = provider_zone_id, hosting_state, excluded.hosting_state),
			soa_serial       = IIF(excluded.soa_serial = 0 AND excluded.provider_zone_id = provider_zone_id, soa_serial, excluded.soa_serial),
			refreshed_at     = IIF(excluded.refreshed_at = 0 AND excluded.provider_zone_id = provider_zone_id, refreshed_at, excluded.refreshed_at),
			nameservers      = IIF(excluded.nameservers = '' AND excluded.provider_zone_id = provider_zone_id, nameservers, excluded.nameservers),
			dnssec           = IIF(excluded.provider_zone_id = provider_zone_id, dnssec, 0),
			hsts             = IIF(excluded.provider_zone_id = provider_zone_id, hsts, 0),
			min_tls          = IIF(excluded.provider_zone_id = provider_zone_id, min_tls, ''),
			status_at        = IIF(excluded.provider_zone_id = provider_zone_id, status_at, 0),
			signing_seen_at  = IIF(excluded.provider_zone_id = provider_zone_id, signing_seen_at, 0)
		RETURNING id, `+zoneStateColumns,
		z.AccountID, z.Name, z.ProviderID, z.Serial, unixOrZero(z.RefreshedAt), joinNames(z.Nameservers), hostingWord(z.State), listing,
	).Scan(scan.dests(&z.ID)...)
	if err != nil {
		return dns.Zone{}, false, fmt.Errorf("put zone %s: %w", z.Name, err)
	}

	scan.fill()

	return z, replaced, nil
}

// ZoneSync is what SyncAccountZones changed.
type ZoneSync struct {
	// Added are the Zones that were not cached before.
	Added []dns.Zone
	// Dropped are the cached Zones the list no longer had; their Records
	// were deleted with them.
	Dropped []dns.Zone
	// Replaced are the cached Zones the list had under another Provider
	// zone ID, as stored now: another zone at the Provider took the name,
	// keeping the Zone's ID but none of its Records or Refresh.
	Replaced []dns.Zone
}

// SyncAccountZones makes an Account's cached Zones match a Zone list from
// its Provider, in one transaction. Each listed Zone is stored as PutZone
// stores it, except that its Hosting State is always stored, Unknown
// included, so a Zone that was already cached keeps its ID, serial, last
// Refresh time and Records, unless its Provider zone ID changed. A cached Zone the list does not have is
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

			stored, replaced, err := putZone(ctx, conn, z, true)
			if err != nil {
				return err
			}

			if _, ok := cached[z.Name]; !ok {
				res.Added = append(res.Added, stored)
			} else if replaced {
				res.Replaced = append(res.Replaced, stored)
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

// RemoveZone deletes a Zone from the cache with its Records, after its
// Provider Offboarded it. Like SaveRefresh, it returns ErrZoneNotCached,
// and removes nothing, unless the cache still has that very Zone: the
// same ID, Account, zone name and Provider zone ID.
func (s *Store) RemoveZone(ctx context.Context, z dns.Zone) error {
	// The schema cascades the delete to the Zone's Records.
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM zones WHERE id = ? AND account_id = ? AND name = ? AND provider_zone_id = ?`,
		z.ID, z.AccountID, z.Name, z.ProviderID)
	if err != nil {
		return fmt.Errorf("remove zone %s: %w", z.Name, err)
	}

	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = ErrZoneNotCached
		}

		return fmt.Errorf("remove zone %s: %w", z.Name, err)
	}

	return nil
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
		SELECT z.id, z.account_id, z.name, z.provider_zone_id, `+zoneStateColumns+`
		FROM zones z JOIN accounts a ON a.id = z.account_id
		ORDER BY z.name, a.provider, a.name`)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	defer rows.Close()

	var out []dns.Zone

	for rows.Next() {
		var z dns.Zone

		scan := zoneScan{z: &z}
		if err := rows.Scan(scan.dests(&z.ID, &z.AccountID, &z.Name, &z.ProviderID)...); err != nil {
			return nil, fmt.Errorf("list zones: %w", err)
		}

		scan.fill()

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
// the cached serial, as PutZone does. It also stores z's Zone Status, as
// read at the Refresh, when z.StatusAt, the time its read began, is set
// and later than the cached one, and keeps the cached one otherwise: when
// the Refresh could not read it, or when it was written meanwhile, such
// as by SaveZoneStatus, and the read may have missed that. Times are
// stored to the second, so a read that began in the second the cached
// one was written keeps it too. It returns the Zone as stored.
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

	var refreshed, statusAt, signingSeen int64

	err = tx.QueryRowContext(ctx, `
		UPDATE zones SET
			soa_serial   = IIF(?1 = 0, soa_serial, ?1),
			refreshed_at = ?2,
			dnssec       = IIF(?7 = 0 OR status_at >= ?7, dnssec, ?8),
			hsts         = IIF(?7 = 0 OR status_at >= ?7, hsts, ?9),
			min_tls      = IIF(?7 = 0 OR status_at >= ?7, min_tls, ?10),
			status_at    = IIF(?7 = 0 OR status_at >= ?7, status_at, ?2)
		WHERE id = ?3 AND account_id = ?4 AND name = ?5 AND provider_zone_id = ?6
		RETURNING soa_serial, refreshed_at, dnssec, hsts, min_tls, status_at, signing_seen_at`,
		serial, unixOrZero(at), z.ID, z.AccountID, z.Name, z.ProviderID,
		unixOrZero(z.StatusAt), z.Status.DNSSEC, z.Status.HSTS, z.Status.MinTLS,
	).Scan(&z.Serial, &refreshed, &z.Status.DNSSEC, &z.Status.HSTS, &z.Status.MinTLS, &statusAt, &signingSeen)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrZoneNotCached
	}

	if err != nil {
		return dns.Zone{}, fmt.Errorf("save refresh: %w", err)
	}

	z.RefreshedAt = timeOrZero(refreshed)
	z.StatusAt = timeOrZero(statusAt)
	z.SigningSeenAt = timeOrZero(signingSeen)

	if err := replaceRecords(ctx, tx, z.ID, records); err != nil {
		return dns.Zone{}, fmt.Errorf("save refresh: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return dns.Zone{}, fmt.Errorf("save refresh: %w", err)
	}

	return z, nil
}

// SaveZoneStatus stores the settings u sets of a Zone's Zone Status, as
// status has them, such as the ones its Provider answered a change of
// them with, and returns the Zone as cached. The settings u leaves nil
// keep their cached value, which a Provider that could not read them
// again would have answered Unknown. The Zone Status is then as of at,
// the time it was written: a Refresh whose read of it began earlier
// does not replace it (see SaveRefresh). The rest of the cache is left
// alone. Like SaveRefresh, it returns ErrZoneNotCached unless the cache
// still has that very Zone: the same ID, Account, zone name and Provider
// zone ID.
func (s *Store) SaveZoneStatus(ctx context.Context, z dns.Zone, u dnsprovider.ZoneUpdate, status dnsprovider.ZoneStatus, at time.Time) (dns.Zone, error) {
	scan := zoneScan{z: &z}

	err := s.db.QueryRowContext(ctx, `
		UPDATE zones SET
			dnssec    = IIF(?1, ?2, dnssec),
			hsts      = IIF(?3, ?4, hsts),
			min_tls   = IIF(?10, ?11, min_tls),
			status_at = MAX(status_at, ?5)
		WHERE id = ?6 AND account_id = ?7 AND name = ?8 AND provider_zone_id = ?9
		RETURNING `+zoneStateColumns,
		u.DNSSEC != nil, status.DNSSEC, u.HSTS != nil, status.HSTS, unixOrZero(at),
		z.ID, z.AccountID, z.Name, z.ProviderID, u.MinTLS != nil, status.MinTLS,
	).Scan(scan.dests()...)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrZoneNotCached
	}

	if err != nil {
		return dns.Zone{}, fmt.Errorf("save zone status: %w", err)
	}

	scan.fill()

	return z, nil
}

// SaveSigningSeen records what dnscli saw of a Zone's signing at at:
// signed sets when it was first seen signed, unless it was already;
// unsigned clears it, so the next signing is waited for afresh. It
// returns the Zone's SigningSeenAt as stored. Like SaveRefresh, it
// returns ErrZoneNotCached unless the cache still has that very Zone.
func (s *Store) SaveSigningSeen(ctx context.Context, z dns.Zone, signed bool, at time.Time) (time.Time, error) {
	var seen int64

	err := s.db.QueryRowContext(ctx, `
		UPDATE zones SET
			signing_seen_at = IIF(?1, IIF(signing_seen_at = 0, ?2, signing_seen_at), 0)
		WHERE id = ?3 AND account_id = ?4 AND name = ?5 AND provider_zone_id = ?6
		RETURNING signing_seen_at`,
		signed, unixOrZero(at), z.ID, z.AccountID, z.Name, z.ProviderID,
	).Scan(&seen)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrZoneNotCached
	}

	if err != nil {
		return time.Time{}, fmt.Errorf("save signing seen: %w", err)
	}

	return timeOrZero(seen), nil
}

// ErrNoRecordID is returned by SaveApply for a change whose Record has no
// Provider record ID: the cache could not tell it from other such Records.
var ErrNoRecordID = errors.New("the Record has no Provider record ID")

// SaveApply updates a Zone's cached Records with the changes its Provider
// confirmed applying, in one transaction. Each change's After is the
// Record as the Provider stored it: an addition inserts it, a modification
// replaces the cached Record with its Before's Provider record ID, and a
// deletion removes that Record. A modification whose Record is no longer
// cached inserts it. A non-zero serial becomes the Zone's cached SOA
// serial; zero keeps the cached one. The rest of the cache is left alone.
//
// Like SaveRefresh, it returns ErrZoneNotCached unless the cache still has
// that very Zone: the same ID, Account, zone name and Provider zone ID.
// It returns ErrNoRecordID, and saves nothing, when a change's Record has
// no Provider record ID to find it by.
func (s *Store) SaveApply(ctx context.Context, z dns.Zone, applied []dns.Change, serial uint32) error {
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
		if (c.Kind != dns.ChangeAdd && c.Before.ProviderID == "") || (c.Kind != dns.ChangeDelete && c.After.ProviderID == "") {
			return fmt.Errorf("save apply: %w", ErrNoRecordID)
		}
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

	if serial != 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE zones SET soa_serial = ? WHERE id = ?`, serial, z.ID); err != nil {
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

// hostingWords are the words the cache stores Hosting States as. They are
// dnscli's own, not the module's numbering, so a database keeps its
// meaning across module versions. Unknown is stored as the empty string.
var hostingWords = map[dnsprovider.HostingState]string{
	dnsprovider.HostingActive:  "active",
	dnsprovider.HostingPending: "pending",
	dnsprovider.HostingMoved:   "moved",
}

// hostingWord is how the cache stores a Hosting State.
func hostingWord(s dnsprovider.HostingState) string {
	return hostingWords[s]
}

// hostingStateOf reads a stored Hosting State. A word this build does not
// know, such as one a later build wrote, reads as Unknown, as if the Zone
// had not been listed yet.
func hostingStateOf(word string) dnsprovider.HostingState {
	for s, w := range hostingWords {
		if w == word {
			return s
		}
	}

	return dnsprovider.HostingUnknown
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

// zoneStateColumns are the columns of a cached Zone's state, in the order
// a zoneScan reads them.
const zoneStateColumns = `soa_serial, refreshed_at, dnssec, hsts, min_tls, status_at, nameservers, hosting_state, signing_seen_at`

// zoneScan reads zoneStateColumns into a Zone: Scan into its dests, then
// fill the Zone.
type zoneScan struct {
	z                                *dns.Zone
	refreshed, statusAt, signingSeen int64
	ns, state                        string
}

// dests are the Scan destinations of zoneStateColumns, after any before.
func (s *zoneScan) dests(before ...any) []any {
	return append(before, &s.z.Serial, &s.refreshed, &s.z.Status.DNSSEC, &s.z.Status.HSTS, &s.z.Status.MinTLS,
		&s.statusAt, &s.ns, &s.state, &s.signingSeen)
}

// fill sets the Zone's state from what was scanned.
func (s *zoneScan) fill() {
	s.z.RefreshedAt = timeOrZero(s.refreshed)
	s.z.StatusAt = timeOrZero(s.statusAt)
	s.z.Nameservers = splitNames(s.ns)
	s.z.State = hostingStateOf(s.state)
	s.z.SigningSeenAt = timeOrZero(s.signingSeen)
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
