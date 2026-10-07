package tui_test

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
)

// seedDuplicate adds example.com to cloudflare.work, so the zone name
// example.com exists in both seeded Accounts.
func seedDuplicate(t *testing.T, s *store.Store) {
	t.Helper()

	accounts, err := s.Accounts(context.Background())
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}

	for _, a := range accounts {
		if a.Label() == "cloudflare.work" {
			mustZone(t, s, dns.Zone{AccountID: a.ID, Name: "example.com", ProviderID: "z4"})
		}
	}
}

// newWideHarness gives the Domains pane room for a zone name and an Account label.
func newWideHarness(t *testing.T, s *store.Store) *harness {
	t.Helper()

	h := newHarness(t, s)
	h.send(tea.WindowSizeMsg{Width: 160, Height: 30})

	return h
}

func TestZoneNameInSeveralAccountsIsShownWithItsAccount(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	seedDuplicate(t, s)

	h := newWideHarness(t, s)

	h.assertShows("example.com · cloudflare.personal", "example.com · cloudflare.work")
}

func TestZoneNamesDifferingOnlyInCaseOrTrailingDotAreTheSameName(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	accounts, err := s.Accounts(context.Background())
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}

	for _, a := range accounts {
		if a.Label() == "cloudflare.work" {
			mustZone(t, s, dns.Zone{AccountID: a.ID, Name: "Example.COM.", ProviderID: "z4"})
		}
	}

	h := newWideHarness(t, s)

	h.assertShows("example.com · cloudflare.personal", "Example.COM. · cloudflare.work")
}

func TestZoneNameInOneAccountIsShownWithoutAnAccount(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	seedDuplicate(t, s)

	h := newWideHarness(t, s)

	h.assertShows("example.org", "two.tv")
	h.assertHides("example.org ·", "two.tv ·")
}
