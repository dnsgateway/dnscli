package tui_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
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

// addWorkAccount adds cloudflare.work, backed by f, in a session that asks r.
func addWorkAccount(t *testing.T, f *fakeProvider, r tui.Resolver) (*harness, *store.Store) {
	t.Helper()

	s, _ := openStore(t)
	h := newHarnessWith(t, s, tui.Deps{
		Providers:    f.registry(),
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
	f := newFake().
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
	f := newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2"})
	f.SetNameservers(cruzDee...)

	h, _ := addWorkAccount(t, f, &fakeResolver{ns: map[string][]string{
		"work.example": cruzDee,
		"work.test":    {"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"},
	}})

	h.assertLive("work.example", true)
	h.assertLive("work.test", false)
}

// nameserversCalls counts how often the Account's nameservers were asked for.
func nameserversCalls(f *fakeProvider) int {
	n := 0

	for _, c := range f.Calls() {
		if c.Op == providertest.OpAccount {
			n++
		}
	}

	return n
}

func TestTheAccountsNameserversAreAskedOncePerSession(t *testing.T) {
	f := newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2"})
	f.SetNameservers(cruzDee...)

	h, _ := addWorkAccount(t, f, &fakeResolver{ns: map[string][]string{"work.example": cruzDee}})

	for range 2 {
		h.press("shift+tab", "L")
		h.assertShows("Re-listed cloudflare.work")
		h.press("tab")
	}

	h.assertLive("work.example", true)

	// Asking costs a request, such as Namecheap's, of a small budget.
	if n := nameserversCalls(f); n != 1 {
		t.Fatalf("Account calls = %d, want 1", n)
	}
}

func TestAZoneListedWithoutNameserversExpectsTheOnesItsAccountsOtherZonesShare(t *testing.T) {
	f := newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2", Nameservers: cruzDee}).
		AddZone(dns.Zone{Name: "work.net", ProviderID: "zw3", Nameservers: []string{"DEE.ns.cloudflare.com.", "cruz.ns.cloudflare.com"}})

	h, _ := addWorkAccount(t, f, &fakeResolver{ns: map[string][]string{"work.example": cruzDee}})

	h.assertLive("work.example", true)

	// The listed Zones already say which nameservers the Account expects:
	// Account was only asked to verify the token.
	if n := nameserversCalls(f); n != 1 {
		t.Fatalf("Account calls = %d, want 1", n)
	}
}

func TestAZoneListedWithoutNameserversInAnAccountWithSeveralSetsHasNoneExpected(t *testing.T) {
	adaBob := []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}
	f := newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2", Nameservers: cruzDee}).
		AddZone(dns.Zone{Name: "work.net", ProviderID: "zw3", Nameservers: adaBob})
	f.SetNameservers(append(adaBob, cruzDee...)...)

	h, s := addWorkAccount(t, f, &fakeResolver{ns: map[string][]string{"work.example": cruzDee}})

	h.assertLive("work.example", false)

	if ns := zoneNamed(t, s, "work.example").Nameservers; len(ns) != 0 {
		t.Fatalf("work.example expects %v, want none cached", ns)
	}

	if n := nameserversCalls(f); n != 1 {
		t.Fatalf("Account calls = %d, want 1, to verify the token", n)
	}
}

func TestAFailureToReadTheAccountsNameserversStillCachesItsZones(t *testing.T) {
	// The Account was added, its token verified, in an earlier session.
	s, _ := enrollFirstAccount(t)

	f := workZones()
	f.Fail(providertest.OpAccount, errors.New("forbidden"))

	h := newHarnessWith(t, s, tui.Deps{
		Providers:    f.registry(),
		UnsealMethod: fastPassphrase(),
		Resolver:     &fakeResolver{ns: map[string][]string{"work.example": cruzDee}},
	})

	h.press("shift+tab", "L")
	h.unsealWith(passphrase)
	h.assertShows("Re-listed cloudflare.work")
	h.press("tab")
	h.assertLive("work.example", false)

	if ns := zoneNamed(t, s, "work.example").Nameservers; len(ns) != 0 {
		t.Fatalf("work.example expects %v, want none cached", ns)
	}
}

// runOnce runs cmd and the commands it batches, without feeding their
// messages back to the model, and returns those messages.
func runOnce(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}

	switch msg := cmd().(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, runOnce(c)...)
		}

		return out
	default:
		return []tea.Msg{msg}
	}
}

func TestDelegationLookupsRunBoundedLikeTheSerialChecks(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	r := &fakeResolver{ns: map[string][]string{}}
	h, startup := newHarnessHoldingStartup(t, s, tui.Deps{Resolver: r, SerialChecks: 2})

	msgs := runOnce(startup)
	if got := r.nsQueries(); len(got) != 2 {
		t.Fatalf("NS queries in flight = %v, want 2", got)
	}

	for _, msg := range msgs {
		h.send(msg)
	}

	if got := r.nsQueries(); len(got) != 3 {
		t.Fatalf("NS queries = %v, want each of the 3 zone names asked", got)
	}
}

func TestAFailedDelegationLookupRunsAgainOnReload(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	expectNameservers(t, s, "cloudflare.personal", "example.com", cruzDee...)

	r := &fakeResolver{
		ns:     map[string][]string{"example.com": cruzDee},
		nsErrs: map[string]error{"example.com": errors.New("i/o timeout")},
	}
	h := newSerialCheckHarness(t, s, r)
	h.assertLive("example.com", false)

	delete(r.nsErrs, "example.com")
	h.reloadCache()

	h.assertLive("example.com", true)
}

var adaBob = []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}

func TestAZoneThatIsNotTheLiveZoneShowsNoSerialCheckVerdict(t *testing.T) {
	s, _ := openStore(t)
	seedSharedName(t, s)

	// The resolver's serial is the Live Zone's: example.com in cloudflare.work.
	// It happens to match the serial cached for cloudflare.personal.
	h := newHarnessWith(t, s, tui.Deps{Resolver: &fakeResolver{
		ns:      map[string][]string{"example.com": cruzDee},
		serials: map[string]uint32{"example.com": 2026100701},
	}})
	h.send(tea.WindowSizeMsg{Width: 160, Height: 30})

	if row := h.domainRow("example.com · cloudflare.personal"); strings.ContainsAny(row, "≈!") {
		t.Fatalf("personal row = %q, want no Serial Check marker", row)
	}

	if row := h.domainRow("example.com · cloudflare.work"); !strings.Contains(row, "!") {
		t.Fatalf("work row = %q, want the possibly-stale marker", row)
	}

	h.press("space")
	h.assertStatusLineShows("no Serial Check: the resolver answers for the Live Zone")
	h.assertHides("serial matches")

	h.press("j", "space")
	h.assertStatusLineShows("possibly stale")
}

func TestADelegationIsResolvedAgainOnReloadOnceItExpires(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	expectNameservers(t, s, "cloudflare.personal", "example.com", cruzDee...)

	now := testNow
	r := &fakeResolver{ns: map[string][]string{"example.com": adaBob}}
	h := newHarnessWith(t, s, tui.Deps{
		Providers:    newFake().registry(),
		UnsealMethod: fastPassphrase(),
		Resolver:     r,
		Clock:        func() time.Time { return now },
	})
	h.assertLive("example.com", false)

	// The Delegation changed at the registrar.
	r.ns["example.com"] = cruzDee

	now = testNow.Add(9 * time.Minute)
	h.reloadCache()
	h.assertLive("example.com", false)

	now = testNow.Add(10 * time.Minute)
	h.fillAddAccount("lab2", "lab2-token")
	h.assertShows("cloudflare.lab2")
	h.assertLive("example.com", true)
}

func TestRelistingAnAccountResolvesItsZoneNamesDelegationAgain(t *testing.T) {
	f := newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Nameservers: cruzDee}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2", Nameservers: cruzDee})

	r := &fakeResolver{ns: map[string][]string{"work.example": adaBob, "work.test": cruzDee}}
	h, _ := addWorkAccount(t, f, r)
	h.assertLive("work.example", false)

	// The Delegation changed at the registrar.
	r.ns["work.example"] = cruzDee

	h.press("shift+tab", "L")
	h.assertShows("Re-listed cloudflare.work")
	h.assertLive("work.example", true)
}

func TestTheZoneStatusBarSaysWhetherTheOpenZoneIsTheLiveZone(t *testing.T) {
	s, _ := openStore(t)
	seedSharedName(t, s)

	h := newHarnessWith(t, s, tui.Deps{Resolver: &fakeResolver{ns: map[string][]string{"example.com": cruzDee}}})
	h.send(tea.WindowSizeMsg{Width: 160, Height: 30})

	// example.com in cloudflare.personal, then in cloudflare.work.
	h.press("space")
	h.assertStatusBarShows("not the Live Zone")

	h.press("j", "space")
	h.assertStatusBarShows("· Live Zone")

	if strings.Contains(h.statusBar(), "not the Live Zone") {
		t.Fatalf("status bar = %q, want example.com in cloudflare.work shown as the Live Zone", h.statusBar())
	}

	// two.tv's Delegation lookup failed.
	h.press("j", "j", "space")
	h.assertStatusBarShows("Live Zone unknown")
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
