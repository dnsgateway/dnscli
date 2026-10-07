package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
)

func open(t *testing.T, path string) *store.Store {
	t.Helper()

	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	return s
}

func TestOpenUsesThePathVerbatim(t *testing.T) {
	for _, name := range []string{"a#b.db", "a?b.db", "a%41b.db", "a b.db"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)

			s := open(t, path)
			if _, err := s.AddAccount(context.Background(), dns.Account{Provider: "cloudflare", Name: "work"}); err != nil {
				t.Fatalf("add account: %v", err)
			}

			if err := s.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}

			if _, err := os.Stat(path); err != nil {
				t.Fatalf("database is not at %q: %v", path, err)
			}

			accounts, err := open(t, path).Accounts(context.Background())
			if err != nil {
				t.Fatalf("accounts: %v", err)
			}

			if len(accounts) != 1 || accounts[0].Label() != "cloudflare.work" {
				t.Fatalf("reopened database has accounts %+v", accounts)
			}
		})
	}
}

func TestConcurrentOpensOfAFreshDatabaseAllSucceed(t *testing.T) {
	for range 20 {
		path := filepath.Join(t.TempDir(), "dnscli.db")

		var wg sync.WaitGroup

		errs := make([]error, 4)
		for i := range errs {
			wg.Add(1)

			go func() {
				defer wg.Done()

				s, err := store.Open(context.Background(), path)
				if err == nil {
					_, err = s.Accounts(context.Background())
					_ = s.Close()
				}

				errs[i] = err
			}()
		}

		wg.Wait()

		for _, err := range errs {
			if err != nil {
				t.Fatalf("concurrent open: %v", err)
			}
		}
	}
}

func TestPutZoneWithoutRefreshMetadataKeepsTheCachedSerialAndRefreshTime(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	a, err := s.AddAccount(ctx, dns.Account{Provider: "cloudflare", Name: "work"})
	if err != nil {
		t.Fatalf("add account: %v", err)
	}

	refreshed := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	first, err := s.PutZone(ctx, dns.Zone{AccountID: a.ID, Name: "example.com", ProviderID: "z1", Serial: 2026100701, RefreshedAt: refreshed})
	if err != nil {
		t.Fatalf("put zone: %v", err)
	}

	// Relisting the Account's Zones knows the zone ID but not the Serial.
	second, err := s.PutZone(ctx, dns.Zone{AccountID: a.ID, Name: "example.com", ProviderID: "z1-new"})
	if err != nil {
		t.Fatalf("put zone again: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("second PutZone created a new Zone: %d != %d", second.ID, first.ID)
	}

	zones, err := s.Zones(ctx)
	if err != nil {
		t.Fatalf("zones: %v", err)
	}

	if len(zones) != 1 {
		t.Fatalf("got %d zones, want 1", len(zones))
	}

	z := zones[0]
	if z.ProviderID != "z1-new" || z.Serial != 2026100701 || !z.RefreshedAt.Equal(refreshed) {
		t.Fatalf("zone after relist = %+v, want provider ID z1-new, serial 2026100701, refreshed %v", z, refreshed)
	}
}

func TestAZoneListOfARemovedAccountIsRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	a, err := s.AddAccount(ctx, dns.Account{Provider: "cloudflare", Name: "work"})
	if err != nil {
		t.Fatalf("add account: %v", err)
	}

	if err := s.RemoveAccount(ctx, a.ID); err != nil {
		t.Fatalf("remove account: %v", err)
	}

	if _, err := s.SyncAccountZones(ctx, a.ID, []dns.Zone{{Name: "example.com", ProviderID: "z1"}}); !errors.Is(err, store.ErrAccountNotFound) {
		t.Fatalf("sync zones of a removed Account: %v, want ErrAccountNotFound", err)
	}

	if _, err := s.AccountSecret(ctx, a.ID); !errors.Is(err, store.ErrAccountNotFound) {
		t.Fatalf("secret of a removed Account: %v, want ErrAccountNotFound", err)
	}
}

func TestARefreshOfAZoneWhoseIDWasReusedIsRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	a, err := s.AddAccount(ctx, dns.Account{Provider: "cloudflare", Name: "work"})
	if err != nil {
		t.Fatalf("add account: %v", err)
	}

	old, err := s.PutZone(ctx, dns.Zone{AccountID: a.ID, Name: "example.com", ProviderID: "z1"})
	if err != nil {
		t.Fatalf("put zone: %v", err)
	}

	// The Account is removed and another added, while a Refresh of old is
	// in flight: the emptied tables hand out the same IDs again.
	if err := s.RemoveAccount(ctx, a.ID); err != nil {
		t.Fatalf("remove account: %v", err)
	}

	b, err := s.AddAccount(ctx, dns.Account{Provider: "cloudflare", Name: "home"})
	if err != nil {
		t.Fatalf("add account: %v", err)
	}

	reused, err := s.PutZone(ctx, dns.Zone{AccountID: b.ID, Name: "example.com", ProviderID: "z9"})
	if err != nil {
		t.Fatalf("put zone: %v", err)
	}

	if reused.ID != old.ID {
		t.Fatalf("the new Zone has ID %d; the test needs it to reuse ID %d", reused.ID, old.ID)
	}

	stale := []dns.Record{{ProviderID: "r1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 300}}
	if err := s.SaveRefresh(ctx, old, stale, 2026100701, time.Now()); !errors.Is(err, store.ErrZoneNotCached) {
		t.Fatalf("save refresh of the removed Zone: %v, want ErrZoneNotCached", err)
	}

	records, err := s.Records(ctx, reused.ID)
	if err != nil || len(records) != 0 {
		t.Fatalf("Records of the new Zone = %+v, %v; want none", records, err)
	}

	zones, err := s.Zones(ctx)
	if err != nil || len(zones) != 1 || zones[0].Serial != 0 || !zones[0].RefreshedAt.IsZero() {
		t.Fatalf("zones = %+v, %v; want the new Zone never Refreshed", zones, err)
	}
}

func TestOpenAcceptsARelativePath(t *testing.T) {
	t.Chdir(t.TempDir())

	if err := os.Mkdir("dnscli", 0o700); err != nil {
		t.Fatal(err)
	}

	open(t, filepath.Join("dnscli", "a#b.db"))

	if _, err := os.Stat(filepath.Join("dnscli", "a#b.db")); err != nil {
		t.Fatalf("database is not at the relative path: %v", err)
	}
}
