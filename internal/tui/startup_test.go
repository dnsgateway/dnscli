package tui_test

import (
	"context"
	"testing"

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
