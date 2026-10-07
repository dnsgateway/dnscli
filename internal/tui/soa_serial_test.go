package tui_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// zoneNS are the nameservers the Provider assigned work.example.
var zoneNS = []string{"cruz.ns.cloudflare.com", "dee.ns.cloudflare.com"}

// serialZones is refreshZones for a Provider that reports no SOA serial,
// with work.example's nameservers as assigned, or none when ns is nil.
func serialZones(ns []string) *fake.Provider {
	f := fake.New().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial, Nameservers: ns}, liveRecords...).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2"})
	f.WithholdSerial(true)

	return f
}

// cacheSerial sets work.example's cached serial, as an earlier session's
// Refresh left it.
func cacheSerial(t *testing.T, h *harness, serial uint32) {
	t.Helper()

	z := cachedZone(t, h.store, "work.example")
	z.Serial = serial

	mustZone(t, h.store, z)
}

func TestARefreshTakesTheSerialTheProviderReportsWithoutAskingTheNameservers(t *testing.T) {
	r := &fakeResolver{soaAt: map[string]uint32{"work.example": 7}}
	h, s, _ := newRefreshHarnessWith(t, refreshZones(), tui.Deps{Resolver: r})

	h.press("enter")
	h.assertShows("validated this session")

	if z := cachedZone(t, s, "work.example"); z.Serial != liveSerial {
		t.Fatalf("cached serial = %d, want the Provider's %d", z.Serial, liveSerial)
	}

	if q := r.soaAtQueries(); len(q) != 0 {
		t.Fatalf("SOA queries to the nameservers = %+v, want none", q)
	}
}

func TestARefreshAsksTheZonesNameserversForTheSerialTheProviderDoesNotReport(t *testing.T) {
	r := &fakeResolver{soaAt: map[string]uint32{"work.example": 2026100799}}
	h, s, _ := newRefreshHarnessWith(t, serialZones(zoneNS), tui.Deps{Resolver: r})

	h.press("enter")
	h.assertShows("validated this session")

	if z := cachedZone(t, s, "work.example"); z.Serial != 2026100799 {
		t.Fatalf("cached serial = %d, want 2026100799 from the nameservers", z.Serial)
	}

	want := []soaAtQuery{{zone: "work.example", servers: zoneNS}}
	if q := r.soaAtQueries(); !reflect.DeepEqual(q, want) {
		t.Fatalf("SOA queries to the nameservers = %+v, want %+v", q, want)
	}
}

func TestARefreshFallsBackToTheDelegatedNameserversForTheSerial(t *testing.T) {
	delegated := []string{"a.ns.example.net", "b.ns.example.net"}
	r := &fakeResolver{
		soaAt: map[string]uint32{"work.example": 2026100799},
		ns:    map[string][]string{"work.example": delegated},
	}
	h, s, _ := newRefreshHarnessWith(t, serialZones(nil), tui.Deps{Resolver: r})

	h.press("enter")
	h.assertShows("validated this session")

	if z := cachedZone(t, s, "work.example"); z.Serial != 2026100799 {
		t.Fatalf("cached serial = %d, want 2026100799 from the nameservers", z.Serial)
	}

	want := []soaAtQuery{{zone: "work.example", servers: delegated}}
	if q := r.soaAtQueries(); !reflect.DeepEqual(q, want) {
		t.Fatalf("SOA queries to the nameservers = %+v, want %+v", q, want)
	}
}

func TestARefreshDoesNotAskTheDelegatedNameserversOfAZoneNameInSeveralAccounts(t *testing.T) {
	r := &fakeResolver{
		soaAt: map[string]uint32{"work.example": 2026100799},
		ns:    map[string][]string{"work.example": {"a.ns.example.net", "b.ns.example.net"}},
	}
	h, _, _ := newRefreshHarnessWith(t, serialZones(nil), tui.Deps{Resolver: r})

	// Another Account has work.example too: the Delegation may name its
	// nameservers, which serve another copy of the zone.
	h.fillAddAccount("lab", "lab-token")
	h.assertShows("work.example · cloudfla")

	h.press("enter")
	h.assertShows("validated this session")

	if q := r.soaAtQueries(); len(q) != 0 {
		t.Fatalf("SOA queries to the nameservers = %+v, want none", q)
	}
}

func TestARefreshWhoseSerialQueryFailsKeepsTheCachedSerial(t *testing.T) {
	r := &fakeResolver{soaAtErrs: map[string]error{"work.example": errors.New("i/o timeout")}}
	h, s, _ := newRefreshHarnessWith(t, serialZones(zoneNS), tui.Deps{Resolver: r})
	cacheSerial(t, h, 2026100700)

	h.press("enter")
	h.assertShows("validated this session")

	if z := cachedZone(t, s, "work.example"); z.Serial != 2026100700 {
		t.Fatalf("cached serial = %d, want the cached 2026100700", z.Serial)
	}
}

func TestAnApplyStoresTheNameserversSerialOnlyWhenItIsNewer(t *testing.T) {
	cases := map[string]struct {
		cached, queried, want uint32
	}{
		"newer":                    {cached: 2026100700, queried: 2026100701, want: 2026100701},
		"not changed yet":          {cached: 2026100700, queried: 2026100700, want: 2026100700},
		"older":                    {cached: 2026100700, queried: 2026100699, want: 2026100700},
		"newer across wrap-around": {cached: 4294967290, queried: 5, want: 5},
		"older across wrap-around": {cached: 5, queried: 4294967290, want: 5},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := &fakeResolver{soaAt: map[string]uint32{"work.example": c.cached}}
			f := serialZones(zoneNS)
			h, s, _ := newRefreshHarnessWith(t, f, tui.Deps{Resolver: r})
			h.press("enter", "tab")
			h.buildChangeset()

			r.setSOAAt("work.example", c.queried)
			h.press("p", "enter")
			h.assertShows("Applied 3 changes to work.example.")

			if z := cachedZone(t, s, "work.example"); z.Serial != c.want {
				t.Fatalf("cached serial = %d, want %d", z.Serial, c.want)
			}
		})
	}
}

func TestAnApplyWhoseSerialQueryFailsKeepsTheCachedSerial(t *testing.T) {
	r := &fakeResolver{soaAt: map[string]uint32{"work.example": 2026100700}}
	f := serialZones(zoneNS)
	h, s, _ := newRefreshHarnessWith(t, f, tui.Deps{Resolver: r})
	h.press("enter", "tab")
	h.buildChangeset()

	r.mu.Lock()
	r.soaAtErrs = map[string]error{"work.example": errors.New("i/o timeout")}
	r.mu.Unlock()

	h.press("p", "enter")
	h.assertShows("Applied 3 changes to work.example.")

	if z := cachedZone(t, s, "work.example"); z.Serial != 2026100700 {
		t.Fatalf("cached serial = %d, want the cached 2026100700", z.Serial)
	}
}
