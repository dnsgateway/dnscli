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

	// Unknown is what the Provider reported, not a missing value: it replaces the cached state too.
	if _, err := s.PutZone(ctx, dns.Zone{AccountID: z.AccountID, Name: "example.com", ProviderID: "z1", State: dnsprovider.HostingMoved}); err != nil {
		t.Fatalf("put zone: %v", err)
	}

	if _, err := s.PutZone(ctx, dns.Zone{AccountID: z.AccountID, Name: "example.com", ProviderID: "z1"}); err != nil {
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
