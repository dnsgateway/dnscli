package tui_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
)

// zonesNamed returns every cached Zone with the zone name, in any Account.
func zonesNamed(t *testing.T, s *store.Store, name string) []dns.Zone {
	t.Helper()

	zones, err := s.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var out []dns.Zone

	for _, z := range zones {
		if z.Name == name {
			out = append(out, z)
		}
	}

	return out
}

// setHostingState caches a Zone's Hosting State, as a listing stores it.
func setHostingState(t *testing.T, s *store.Store, zone string, state dnsprovider.HostingState) {
	t.Helper()

	z := zoneNamed(t, s, zone)
	z.State = state
	mustZone(t, s, z)
}

func TestTheDomainsPaneMarksPendingAndMovedZones(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	setHostingState(t, s, "example.com", dnsprovider.HostingActive)
	setHostingState(t, s, "two.tv", dnsprovider.HostingPending)
	setHostingState(t, s, "example.org", dnsprovider.HostingMoved)

	h := newWideHarness(t, s)

	if row := h.domainRow("two.tv"); !strings.Contains(row, "two.tv · pending") {
		t.Errorf("two.tv row = %q, want it marked pending", row)
	}

	if row := h.domainRow("example.org"); !strings.Contains(row, "example.org · moved") {
		t.Errorf("example.org row = %q, want it marked moved", row)
	}

	// An active Zone looks as it did before Hosting State was known.
	if row := h.domainRow("example.com"); strings.Contains(row, "·") || strings.Contains(row, "active") {
		t.Errorf("example.com row = %q, want no Hosting State mark", row)
	}
}

func TestAZoneWhoseHostingStateIsUnknownIsNotMarked(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newWideHarness(t, s)

	for _, zone := range []string{"example.com", "two.tv", "example.org"} {
		if row := h.domainRow(zone); strings.Contains(row, "·") {
			t.Errorf("%s row = %q, want no Hosting State mark", zone, row)
		}
	}
}

func TestTheHostingStateMarkFollowsTheAccountOfASharedZoneName(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	seedDuplicate(t, s)

	for _, z := range zonesNamed(t, s, "example.com") {
		if z.ProviderID == "z4" {
			z.State = dnsprovider.HostingPending
			mustZone(t, s, z)
		}
	}

	h := newWideHarness(t, s)

	// The label is cut short to keep the mark in view.
	if row := h.domainRow("example.com · cloudflar…"); !strings.HasSuffix(strings.TrimRight(row, " "), "cloudflar… · pending") {
		t.Errorf("work row = %q, want its label cut short and marked pending", row)
	}

	if row := h.domainRow("example.com · cloudflare.personal"); strings.Contains(row, "pending") {
		t.Errorf("personal row = %q, want no mark", row)
	}
}
