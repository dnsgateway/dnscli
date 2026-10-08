package store_test

import (
	"context"
	"database/sql"
	"errors"
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

func TestAZoneRelistedWithAnotherProviderIDForgetsItsRecordsAndRefresh(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	ns := []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}
	z := putZone(t, s, dns.Zone{Name: "example.com", ProviderID: "z1", Nameservers: ns, State: dnsprovider.HostingActive})

	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	records := []dns.Record{{ProviderID: "r1", Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 300}}

	if _, err := s.SaveRefresh(ctx, z, records, 2026100701, at); err != nil {
		t.Fatalf("save refresh: %v", err)
	}

	// The zone was deleted and created again at the Provider; the listing
	// says nothing of its nameservers.
	sync, err := s.SyncAccountZones(ctx, z.AccountID, []dns.Zone{{Name: "example.com", ProviderID: "z2", State: dnsprovider.HostingPending}})
	if err != nil {
		t.Fatalf("sync zones: %v", err)
	}

	got := onlyZone(t, s)
	if got.ID != z.ID || got.ProviderID != "z2" || !got.RefreshedAt.IsZero() || got.Nameservers != nil || got.State != dnsprovider.HostingPending {
		t.Fatalf("zone = %+v, want z2, pending, never Refreshed, no nameservers", got)
	}

	if cached, err := s.Records(ctx, z.ID); err != nil || len(cached) != 0 {
		t.Fatalf("records = %+v, %v; want none", cached, err)
	}

	if len(sync.Added) != 0 || len(sync.Dropped) != 0 || len(sync.Replaced) != 1 || sync.Replaced[0].ID != z.ID || sync.Replaced[0].ProviderID != "z2" {
		t.Fatalf("sync = %+v, want only example.com replaced by z2", sync)
	}
}

// on is a setting turned on, for a ZoneUpdate.
var on = true

func TestSaveZoneStatusStoresTheSettingsTheUpdateSetsAndReturnsTheZone(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	refreshed := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	z := putZone(t, s, dns.Zone{Name: "example.com", ProviderID: "z1", Serial: 2026100701, RefreshedAt: refreshed})

	z.Status, z.StatusAt = dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.On}, refreshed

	z, err := s.SaveRefresh(ctx, z, nil, 0, refreshed)
	if err != nil {
		t.Fatal(err)
	}

	// The Provider could not read HSTS again after turning DNSSEC on.
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	saved, err := s.SaveZoneStatus(ctx, z, dnsprovider.ZoneUpdate{DNSSEC: &on},
		dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Pending, HSTS: dnsprovider.Unknown}, at)
	if err != nil {
		t.Fatalf("save zone status: %v", err)
	}

	want := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Pending, HSTS: dnsprovider.On}
	if saved.ID != z.ID || saved.Status != want || !saved.StatusAt.Equal(at) || saved.Serial != 2026100701 || !saved.RefreshedAt.Equal(refreshed) {
		t.Fatalf("saved = %+v, want the Zone with DNSSEC pending, HSTS kept, written at %v", saved, at)
	}

	if got := onlyZone(t, s); got.Status != want || !got.StatusAt.Equal(at) || got.Serial != 2026100701 || !got.RefreshedAt.Equal(refreshed) {
		t.Fatalf("cached = %+v, want DNSSEC pending, HSTS kept, the serial and Refresh kept", got)
	}
}

func TestARefreshWhoseZoneStatusReadBeganBeforeTheCachedOneWasWrittenKeepsTheCachedOne(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	z := putZone(t, s, dns.Zone{Name: "example.com", ProviderID: "z1"})

	// DNSSEC is turned on while a Refresh that read it off is in flight.
	written := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	if _, err := s.SaveZoneStatus(ctx, z, dnsprovider.ZoneUpdate{DNSSEC: &on}, dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Pending}, written); err != nil {
		t.Fatal(err)
	}

	refreshed := written.Add(2 * time.Second)

	for _, began := range []time.Time{written.Add(-time.Second), written} {
		z.Status, z.StatusAt = dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.Off}, began

		got, err := s.SaveRefresh(ctx, z, nil, 0, refreshed)
		if err != nil {
			t.Fatal(err)
		}

		if got.Status.DNSSEC != dnsprovider.Pending || !got.StatusAt.Equal(written) || !got.RefreshedAt.Equal(refreshed) {
			t.Fatalf("read began %v: refreshed = %+v, want the Zone Status written at %v kept, Refreshed at %v", began, got, written, refreshed)
		}
	}

	// One whose read began after it is newer, and read at the Refresh.
	z.Status, z.StatusAt = dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.Off}, written.Add(time.Second)

	got, err := s.SaveRefresh(ctx, z, nil, 0, refreshed)
	if err != nil {
		t.Fatal(err)
	}

	if got.Status != z.Status || !got.StatusAt.Equal(refreshed) {
		t.Fatalf("refreshed = %+v, want its Zone Status, read at %v", got, refreshed)
	}

	if cached := onlyZone(t, s); cached.Status != z.Status || !cached.StatusAt.Equal(refreshed) {
		t.Fatalf("cached = %+v, want the newer Zone Status", cached)
	}
}

func TestSaveZoneStatusOfAZoneNoLongerCachedIsErrZoneNotCached(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	z := putZone(t, s, dns.Zone{Name: "example.com", ProviderID: "z1"})

	// Another zone took the name at the Provider meanwhile.
	if _, err := s.PutZone(ctx, dns.Zone{AccountID: z.AccountID, Name: "example.com", ProviderID: "z2"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SaveZoneStatus(ctx, z, dnsprovider.ZoneUpdate{DNSSEC: &on}, dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On}, time.Now()); !errors.Is(err, store.ErrZoneNotCached) {
		t.Fatalf("save zone status error = %v, want ErrZoneNotCached", err)
	}

	if got := onlyZone(t, s); got.Status != (dnsprovider.ZoneStatus{}) {
		t.Fatalf("cached status = %+v, want none", got.Status)
	}
}
