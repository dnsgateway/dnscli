package tui_test

import (
	"context"
	"testing"
	"time"

	"github.com/sergelogvinov/dnscli/internal/store"
)

func TestEmptyDatabaseShowsAddFirstAccountPrompt(t *testing.T) {
	s, _ := openStore(t)
	h := newHarness(t, s)

	h.assertShows("Domains", "Accounts", "Records", "add your first Account")
}

func TestStartupRendersCachedAccountsAndZones(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.assertShows("cloudflare.personal", "cloudflare.work", "example.com", "two.tv", "example.org")
	h.assertHides("add your first Account")
}

func TestCachedZonesStartDimmed(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.assertDimmed("example.com")
	h.assertDimmed("two.tv")
	h.assertDimmed("example.org")
}

func TestBrowsingLeavesTheCacheUnchanged(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("enter", "j", "j", "enter", "tab", "tab", "j", "?", "esc")

	zones, err := s.Zones(context.Background())
	if err != nil {
		t.Fatalf("zones: %v", err)
	}

	want := map[string]int{"example.com": 3, "example.org": 0, "two.tv": 1}
	if len(zones) != len(want) {
		t.Fatalf("got %d zones, want %d", len(zones), len(want))
	}

	for _, z := range zones {
		records, err := s.Records(context.Background(), z.ID)
		if err != nil {
			t.Fatalf("records: %v", err)
		}

		if len(records) != want[z.Name] {
			t.Fatalf("%s has %d records, want %d", z.Name, len(records), want[z.Name])
		}

		for _, r := range records {
			if r.Type == "CNAME" && r.Extras["proxied"] != "true" {
				t.Fatalf("CNAME lost its proxied extra: %+v", r)
			}
		}

		if z.Name == "example.com" && (z.Serial != 2026100701 || !z.RefreshedAt.Equal(testNow.Add(-3*time.Hour))) {
			t.Fatalf("example.com serial/refresh changed: %+v", z)
		}
	}
}

func TestStoreFailureIsShownInTheUI(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	h := newHarness(t, s)

	h.assertShows("list accounts")
	h.assertHides("add your first Account")
}

func TestCacheSurvivesRestart(t *testing.T) {
	s, path := openStore(t)
	seedCache(t, s)

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	h := newHarness(t, reopened)

	h.assertShows("cloudflare.personal", "example.com", "two.tv")
}
