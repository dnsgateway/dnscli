package tui_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
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

func TestAZoneWhoseNameserversDifferFromTheDelegationIsNotTheLiveZone(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	expectNameservers(t, s, "cloudflare.personal", "example.com", "ada.ns.cloudflare.com", "bob.ns.cloudflare.com")

	// One nameserver in common is not a match: the sets must be equal.
	h := newHarnessWith(t, s, tui.Deps{Resolver: &fakeResolver{ns: map[string][]string{
		"example.com": {"ada.ns.cloudflare.com", "ns1.registrar.example"},
	}}})

	h.assertLive("example.com", false)
}

// seedSharedName puts example.com in both seeded Accounts, each expecting
// its own nameservers, as Cloudflare assigns them per Zone.
func seedSharedName(t *testing.T, s *store.Store) {
	t.Helper()

	seedCache(t, s)
	seedDuplicate(t, s)
	expectNameservers(t, s, "cloudflare.personal", "example.com", "ada.ns.cloudflare.com", "bob.ns.cloudflare.com")
	expectNameservers(t, s, "cloudflare.work", "example.com", "cruz.ns.cloudflare.com", "dee.ns.cloudflare.com")
}

func TestAmongZonesSharingAZoneNameOnlyTheOneMatchingTheDelegationIsLive(t *testing.T) {
	s, _ := openStore(t)
	seedSharedName(t, s)

	r := &fakeResolver{ns: map[string][]string{"example.com": {"cruz.ns.cloudflare.com", "dee.ns.cloudflare.com"}}}
	h := newHarnessWith(t, s, tui.Deps{Resolver: r})
	h.send(tea.WindowSizeMsg{Width: 160, Height: 30})

	h.assertLive("example.com · cloudflare.work", true)
	h.assertLive("example.com · cloudflare.personal", false)

	// The Delegation belongs to the zone name: it is looked up once.
	asked := 0
	for _, q := range r.nsQueries() {
		if q == "example.com" {
			asked++
		}
	}

	if asked != 1 {
		t.Fatalf("NS queries = %v, want example.com asked once", r.nsQueries())
	}
}

func TestTheDelegationViewShowsTheDelegatedAndTheExpectedNameservers(t *testing.T) {
	s, _ := openStore(t)
	seedSharedName(t, s)

	h := newHarnessWith(t, s, tui.Deps{Resolver: &fakeResolver{ns: map[string][]string{
		"example.com": {"cruz.ns.cloudflare.com.", "dee.ns.cloudflare.com."},
	}}})
	h.send(tea.WindowSizeMsg{Width: 160, Height: 30})

	// The highlighted Zone is example.com in cloudflare.personal.
	h.press("N")
	h.assertShows(
		"Delegation of example.com",
		"cruz.ns.cloudflare.com, dee.ns.cloudflare.com",
		"cloudflare.personal",
		"ada.ns.cloudflare.com, bob.ns.cloudflare.com",
		"not the Live Zone",
		"Live Zone",
		"read-only",
	)

	// It changes nothing: Delegation is read-only (ADR-0004).
	h.press("enter", "d", "e")
	h.assertShows("Delegation of example.com")

	h.press("esc")
	h.assertHides("Delegation of example.com")
}

func TestTheDelegationViewSaysWhyTheLiveZoneIsUnknown(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	expectNameservers(t, s, "cloudflare.personal", "example.com", "ada.ns.cloudflare.com", "bob.ns.cloudflare.com")

	h := newHarnessWith(t, s, tui.Deps{Resolver: &fakeResolver{
		ns:     map[string][]string{"example.org": {"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}},
		nsErrs: map[string]error{"example.com": errors.New("i/o timeout")},
	}})

	h.press("N")
	h.assertShows("Delegation lookup failed: i/o timeout", "ada.ns.cloudflare.com, bob.ns.cloudflare.com", "Live Zone unknown")
	h.press("esc")

	// example.org's expected nameservers were never cached.
	h.press("j", "N")
	h.assertShows("Delegation of example.org", "not cached", "Live Zone unknown")
}

// addWorkAccount adds cloudflare.work, backed by f, in a session that asks r.
func addWorkAccount(t *testing.T, f *fake.Provider, r tui.Resolver) (*harness, *store.Store) {
	t.Helper()

	s, _ := openStore(t)
	h := newHarnessWith(t, s, tui.Deps{
		Providers:    provider.Registry{"cloudflare": f.New},
		UnsealMethod: fastPassphrase(),
		Resolver:     r,
	})
	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")
	h.assertShows("work.example", "work.test")

	return h, s
}

var cruzDee = []string{"cruz.ns.cloudflare.com", "dee.ns.cloudflare.com"}

func TestZoneDiscoveryCachesTheNameserversEachZoneExpects(t *testing.T) {
	f := fake.New().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Nameservers: cruzDee}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2", Nameservers: []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}})

	h, s := addWorkAccount(t, f, &fakeResolver{ns: map[string][]string{"work.example": cruzDee, "work.test": cruzDee}})

	h.assertLive("work.example", true)
	h.assertLive("work.test", false)

	// A later, sealed session marks the Live Zone from the cache alone.
	later := newHarnessWith(t, s, tui.Deps{Resolver: &fakeResolver{ns: map[string][]string{"work.example": cruzDee}}})
	later.assertShows("sealed")
	later.assertLive("work.example", true)
}

func TestZonesListedWithoutNameserversExpectTheAccountsNameservers(t *testing.T) {
	f := fake.New().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2", Nameservers: []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}})
	f.SetNameservers(cruzDee...)

	h, _ := addWorkAccount(t, f, &fakeResolver{ns: map[string][]string{"work.example": cruzDee, "work.test": cruzDee}})

	h.assertLive("work.example", true)
	// A Zone's own nameservers outrank the Account's.
	h.assertLive("work.test", false)
}

func TestAFailureToReadTheAccountsNameserversStillCachesItsZones(t *testing.T) {
	f := fake.New().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2"})
	f.Fail(fake.OpNameservers, errors.New("forbidden"))

	h, _ := addWorkAccount(t, f, &fakeResolver{ns: map[string][]string{"work.example": cruzDee}})

	h.assertShows("Discovered 2 Zones for cloudflare.work.")
	h.assertLive("work.example", false)

	h.press("N")
	h.assertShows("not cached", "Live Zone unknown")
}

func TestAResolverFailureLeavesTheLiveZoneUnknownWithoutAnError(t *testing.T) {
	s, _ := openStore(t)
	seedSharedName(t, s)

	h := newHarnessWith(t, s, tui.Deps{Resolver: &fakeResolver{
		nsErrs: map[string]error{"example.com": errors.New("i/o timeout")},
	}})
	h.send(tea.WindowSizeMsg{Width: 160, Height: 30})

	h.assertLive("example.com · cloudflare.work", false)
	h.assertLive("example.com · cloudflare.personal", false)
	h.assertHides("i/o timeout")
	h.assertShows("? help")
}
