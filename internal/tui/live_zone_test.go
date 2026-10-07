package tui_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// liveMarker is drawn after the Live Zone in the Domains pane.
const liveMarker = "●"

// expectNameservers caches the nameservers an Account expects for its Zone,
// as Zone discovery stores them.
func expectNameservers(t *testing.T, s *store.Store, account, zone string, ns ...string) {
	t.Helper()

	ctx := context.Background()

	accounts, err := s.Accounts(ctx)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}

	zones, err := s.Zones(ctx)
	if err != nil {
		t.Fatalf("zones: %v", err)
	}

	for _, a := range accounts {
		for _, z := range zones {
			if a.Label() == account && z.AccountID == a.ID && z.Name == zone {
				z.Nameservers = ns
				mustZone(t, s, z)

				return
			}
		}
	}

	t.Fatalf("no Zone %s in %s", zone, account)
}

func (h *harness) assertLive(zone string, live bool) {
	h.t.Helper()

	if row := h.domainRow(zone); strings.Contains(row, liveMarker) != live {
		h.t.Fatalf("%s row = %q, want Live Zone marker: %v", zone, row, live)
	}
}

func TestAZoneWhoseNameserversMatchTheDelegationIsTheLiveZone(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	expectNameservers(t, s, "cloudflare.personal", "example.com", "ada.ns.cloudflare.com", "bob.ns.cloudflare.com")

	// Names compare ignoring case and a trailing dot, in any order.
	h := newHarnessWith(t, s, tui.Deps{Resolver: &fakeResolver{ns: map[string][]string{
		"example.com": {"BOB.ns.cloudflare.com.", "ada.ns.cloudflare.com."},
	}}})

	h.assertLive("example.com", true)
	h.assertLive("two.tv", false)
}

