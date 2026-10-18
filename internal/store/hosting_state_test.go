// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

func TestTheHostingStateRoundTripsAndEachListingReplacesIt(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	z := putZone(t, s, dns.Zone{Name: "example.com", ProviderID: "z1", State: dnsprovider.HostingPending})
	if z.State != dnsprovider.HostingPending {
		t.Fatalf("stored zone = %+v, want pending", z)
	}

	if got := onlyZone(t, s); got.State != dnsprovider.HostingPending {
		t.Fatalf("zone = %+v, want pending", got)
	}

	// Delegation reached the Provider: the next listing says active.
	if _, err := s.SyncAccountZones(ctx, z.AccountID, []dns.Zone{{Name: "example.com", ProviderID: "z1", State: dnsprovider.HostingActive}}); err != nil {
		t.Fatalf("sync zones: %v", err)
	}

	if got := onlyZone(t, s); got.State != dnsprovider.HostingActive {
		t.Fatalf("zone after relist = %+v, want active", got)
	}

	// Unknown is what the Provider listed, not a missing value: a listing
	// replaces the cached state with it too.
	if _, err := s.SyncAccountZones(ctx, z.AccountID, []dns.Zone{{Name: "example.com", ProviderID: "z1"}}); err != nil {
		t.Fatalf("sync zones: %v", err)
	}

	if got := onlyZone(t, s); got.State != dnsprovider.HostingUnknown {
		t.Fatalf("zone after an Unknown listing = %+v, want unknown", got)
	}
}

// PutZone is not a listing: like a zero Serial, an Unknown Hosting State
// keeps the cached one, unless the Provider zone ID changed.
func TestPutZoneWithoutAHostingStateKeepsTheCachedOne(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	z := putZone(t, s, dns.Zone{Name: "example.com", ProviderID: "z1", State: dnsprovider.HostingMoved})

	stored, _, err := s.PutZone(ctx, dns.Zone{AccountID: z.AccountID, Name: "example.com", ProviderID: "z1"})
	if err != nil {
		t.Fatalf("put zone: %v", err)
	}

	if got := onlyZone(t, s); got.State != dnsprovider.HostingMoved || stored.State != dnsprovider.HostingMoved {
		t.Fatalf("zone = %+v, returned %+v; want moved kept", got, stored)
	}

	if _, _, err := s.PutZone(ctx, dns.Zone{AccountID: z.AccountID, Name: "example.com", ProviderID: "z1", State: dnsprovider.HostingActive}); err != nil {
		t.Fatalf("put zone: %v", err)
	}

	if got := onlyZone(t, s); got.State != dnsprovider.HostingActive {
		t.Fatalf("zone = %+v, want active", got)
	}

	// Another zone at the Provider: the old one's state is not its.
	if _, _, err := s.PutZone(ctx, dns.Zone{AccountID: z.AccountID, Name: "example.com", ProviderID: "z2"}); err != nil {
		t.Fatalf("put zone: %v", err)
	}

	if got := onlyZone(t, s); got.State != dnsprovider.HostingUnknown {
		t.Fatalf("zone = %+v, want unknown", got)
	}
}

func TestZonesCachedBeforeHostingStateStartAsUnknown(t *testing.T) {
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

	if got := onlyZone(t, open(t, path)); got.Name != "example.com" || got.State != dnsprovider.HostingUnknown {
		t.Fatalf("zone = %+v, want example.com with Hosting State unknown", got)
	}
}

// The cache stores each Hosting State as dnscli's own word, so a database
// keeps its meaning whatever the module numbers them; a value this build
// does not know reads as Unknown.
func TestTheStoredHostingStateIsAStableWordAndAnUnknownOneReadsAsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dnscli.db")
	s := open(t, path)
	putZone(t, s, dns.Zone{Name: "example.com", ProviderID: "z1"})

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for stored, want := range map[any]dnsprovider.HostingState{
		"active":  dnsprovider.HostingActive,
		"pending": dnsprovider.HostingPending,
		"moved":   dnsprovider.HostingMoved,
		"":        dnsprovider.HostingUnknown,
		"retired": dnsprovider.HostingUnknown,
		2:         dnsprovider.HostingUnknown,
	} {
		if _, err := db.Exec(`UPDATE zones SET hosting_state = ?`, stored); err != nil {
			t.Fatal(err)
		}

		if got := onlyZone(t, s); got.State != want {
			t.Errorf("hosting_state %#v reads as %v, want %v", stored, got.State, want)
		}
	}
}
