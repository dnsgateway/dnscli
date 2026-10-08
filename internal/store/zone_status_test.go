package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
)

// putZone stores a Zone in a new Account, cloudflare.work.
func putZone(t *testing.T, s *store.Store, z dns.Zone) dns.Zone {
	t.Helper()

	ctx := context.Background()

	a, err := s.AddAccount(ctx, dns.Account{Provider: "cloudflare", Name: "work"})
	if err != nil {
		t.Fatalf("add account: %v", err)
	}

	z.AccountID = a.ID

	z, err = s.PutZone(ctx, z)
	if err != nil {
		t.Fatalf("put zone: %v", err)
	}

	return z
}

func onlyZone(t *testing.T, s *store.Store) dns.Zone {
	t.Helper()

	zones, err := s.Zones(context.Background())
	if err != nil || len(zones) != 1 {
		t.Fatalf("zones = %+v, %v; want one", zones, err)
	}

	return zones[0]
}

func TestTheZoneStatusAndNameserversRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	ns := []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}
	z := putZone(t, s, dns.Zone{Name: "example.com", ProviderID: "z1", Nameservers: ns})

	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	z.Status, z.StatusAt = dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.Off}, at

	if _, err := s.SaveRefresh(ctx, z, nil, 2026100701, at); err != nil {
		t.Fatalf("save refresh: %v", err)
	}

	got := onlyZone(t, s)
	if got.Status != (dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.Off}) || !got.StatusAt.Equal(at) || !slices.Equal(got.Nameservers, ns) {
		t.Fatalf("zone = %+v, want DNSSEC on, HSTS off, read at %v, nameservers %v", got, at, ns)
	}

	// A relist that lists no nameservers keeps the cached ones and the status.
	if _, err := s.PutZone(ctx, dns.Zone{AccountID: z.AccountID, Name: "example.com", ProviderID: "z1"}); err != nil {
		t.Fatalf("put zone again: %v", err)
	}

	if again := onlyZone(t, s); again.Status != got.Status || !again.StatusAt.Equal(at) || !slices.Equal(again.Nameservers, ns) {
		t.Fatalf("zone after relist = %+v, want %+v", again, got)
	}
}

func TestAZoneRelistedWithAnotherProviderIDForgetsItsStatusAndSerial(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	z := putZone(t, s, dns.Zone{Name: "example.com", ProviderID: "z1"})

	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	z.Status, z.StatusAt = dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.On}, at

	if _, err := s.SaveRefresh(ctx, z, nil, 2026100701, at); err != nil {
		t.Fatalf("save refresh: %v", err)
	}

	// The zone was deleted and created again at the Provider.
	if _, err := s.PutZone(ctx, dns.Zone{AccountID: z.AccountID, Name: "example.com", ProviderID: "z2"}); err != nil {
		t.Fatalf("put zone again: %v", err)
	}

	got := onlyZone(t, s)
	if got.ProviderID != "z2" || got.Serial != 0 || got.Status != (dnsprovider.ZoneStatus{}) || !got.StatusAt.IsZero() {
		t.Fatalf("zone = %+v, want provider ID z2 with no serial and no Zone Status", got)
	}
}

// v2Database is a database as migrations 1 and 2 left it, with one Zone.
const v2Database = `
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
		refreshed_at     INTEGER NOT NULL DEFAULT 0,
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
		extras             TEXT NOT NULL DEFAULT '{}'
	);

	CREATE INDEX records_zone ON records(zone_id);

	ALTER TABLE accounts ADD COLUMN secret BLOB;

	CREATE TABLE key_slots (
		id      INTEGER PRIMARY KEY,
		method  TEXT NOT NULL,
		params  BLOB NOT NULL,
		wrapped BLOB NOT NULL
	);

	INSERT INTO accounts (id, provider, name) VALUES (1, 'cloudflare', 'work');
	INSERT INTO zones (account_id, name, provider_zone_id, soa_serial, refreshed_at)
		VALUES (1, 'example.com', 'z1', 2026100701, 1791363600);

	PRAGMA user_version = 2;
`

func TestAVersion2DatabaseMigratesWithNoZoneStatusOrNameservers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dnscli.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open v2 database: %v", err)
	}

	if _, err := db.Exec(v2Database); err != nil {
		t.Fatalf("create v2 database: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close v2 database: %v", err)
	}

	got := onlyZone(t, open(t, path))
	if got.Name != "example.com" || got.ProviderID != "z1" || got.Serial != 2026100701 || !got.RefreshedAt.Equal(time.Unix(1791363600, 0)) {
		t.Fatalf("zone = %+v, want example.com with its serial and Refresh time kept", got)
	}

	if got.Status != (dnsprovider.ZoneStatus{}) || !got.StatusAt.IsZero() || got.Nameservers != nil {
		t.Fatalf("zone = %+v, want no Zone Status and no nameservers", got)
	}
}
