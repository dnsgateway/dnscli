package tui_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// The nameservers in the Offboarding tests: work.example is served by
// cloudflare.work (its Live Zone), work.test is delegated to its
// registrar's nameservers, so cloudflare.work's work.test is not Live.
var (
	workExampleNS = []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}
	workTestNS    = []string{"cruz.ns.cloudflare.com", "dee.ns.cloudflare.com"}
	registrarNS   = []string{"ns1.registrar.example", "ns2.registrar.example"}
)

// offboardFake is workZones as a ZoneDeleter, each Zone with the
// nameservers Cloudflare assigned it: work.example Active, work.test
// Pending, waiting for a Delegation that never came.
func offboardFake() *fakeProvider {
	f := newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Nameservers: workExampleNS}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2", Nameservers: workTestNS})
	f.SetHostingState("zw2", dnsprovider.HostingPending)
	f.SetZoneDeleter(true)

	return f
}

// offboardResolver delegates work.example to cloudflare.work's Zone and
// work.test to its registrar.
func offboardResolver() *fakeResolver {
	return &fakeResolver{ns: map[string][]string{
		"work.example": slices.Clone(workExampleNS),
		"work.test":    slices.Clone(registrarNS),
	}}
}

// offboardStore is cloudflare.work enrolled, with its Zones' nameservers
// and Hosting States cached as a listing caches them, as offboardFake has
// them.
func offboardStore(t *testing.T) *store.Store {
	t.Helper()

	s, _ := enrollFirstAccount(t)
	expectNameservers(t, s, "cloudflare.work", "work.example", workExampleNS...)
	expectNameservers(t, s, "cloudflare.work", "work.test", workTestNS...)
	setHostingState(t, s, "work.example", dnsprovider.HostingActive)
	setHostingState(t, s, "work.test", dnsprovider.HostingPending)

	return s
}

func newOffboardHarness(t *testing.T, s *store.Store, f *fakeProvider, r *fakeResolver) *harness {
	t.Helper()

	return newHarnessWith(t, s, tui.Deps{Providers: f.registry(), UnsealMethod: fastPassphrase(), Resolver: r})
}

// deleteZoneCalls are the Provider zone IDs the fake was asked to Offboard.
func deleteZoneCalls(f *fakeProvider) []string {
	var out []string

	for _, c := range f.Calls() {
		if c.Op == providertest.OpDeleteZone {
			out = append(out, c.Zone.ID)
		}
	}

	return out
}

// zoneCached reports whether the cache holds a Zone of that name.
func zoneCached(t *testing.T, s *store.Store, name string) bool {
	t.Helper()

	zones, err := s.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	return slices.ContainsFunc(zones, func(z dns.Zone) bool { return z.Name == name })
}

func TestOffboardingAZoneThatIsNotLiveAsksToConfirmThenDropsItFromTheCacheAndThePane(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	h := newOffboardHarness(t, s, f, offboardResolver())

	// Offboarding is a Zone write: the sealed session Unseals first.
	h.press("j", "d")

	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("the Provider was called while sealed: %+v", calls)
	}

	h.unsealWith(passphrase)
	h.assertShows("Offboard work.test from cloudflare.work?", "drops its Records", "registration")

	if calls := deleteZoneCalls(f); len(calls) != 0 {
		t.Fatalf("DeleteZone before confirming: %q", calls)
	}

	h.press("enter")

	if calls := deleteZoneCalls(f); !slices.Equal(calls, []string{"zw2"}) {
		t.Fatalf("DeleteZone calls = %q, want zw2 once", calls)
	}

	h.assertShows("work.test is Offboarded from cloudflare.work")
	h.assertHides("Offboard work.test from", "work.test ·")

	if zoneCached(t, s, "work.test") {
		t.Fatal("work.test is still cached")
	}

	if !zoneCached(t, s, "work.example") {
		t.Fatal("work.example left the cache too")
	}
}

func TestEscClosesTheOffboardDialogOffboardingNothing(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	h := newOffboardHarness(t, s, f, offboardResolver())

	h.press("j", "d")
	h.unsealWith(passphrase)
	h.assertShows("Offboard work.test from cloudflare.work?")

	h.press("esc")
	h.assertHides("Offboard work.test from")

	if calls := deleteZoneCalls(f); len(calls) != 0 || !zoneCached(t, s, "work.test") {
		t.Fatalf("DeleteZone calls = %q, want none and work.test cached", calls)
	}
}

func TestTheLiveZoneIsNeverOffboarded(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	h := newOffboardHarness(t, s, f, offboardResolver())

	// work.example is highlighted: its Delegation names its nameservers.
	h.press("d")

	h.assertShows("work.example is the Live Zone")
	h.assertHides("Unseal", "Offboard work.example from")

	if calls := deleteZoneCalls(f); len(calls) != 0 {
		t.Fatalf("DeleteZone calls = %q, want none", calls)
	}
}

func TestOffboardingIsRefusedWhileTheLiveZoneIsUnknown(t *testing.T) {
	cases := map[string]func(*testing.T, *fakeResolver) *store.Store{
		"delegation not resolved": func(t *testing.T, r *fakeResolver) *store.Store {
			delete(r.ns, "work.test")

			return offboardStore(t)
		},
		"nameservers not cached": func(t *testing.T, _ *fakeResolver) *store.Store {
			s, _ := enrollFirstAccount(t)
			expectNameservers(t, s, "cloudflare.work", "work.example", workExampleNS...)

			return s
		},
	}

	for name, unknown := range cases {
		t.Run(name, func(t *testing.T) {
			r := offboardResolver()
			s := unknown(t, r)

			f := offboardFake()
			h := newOffboardHarness(t, s, f, r)

			h.press("j", "d")

			h.assertShows("Offboarding is refused", "Live Zone", "unknown")
			h.assertHides("Unseal", "Offboard work.test from")

			if calls := deleteZoneCalls(f); len(calls) != 0 {
				t.Fatalf("DeleteZone calls = %q, want none", calls)
			}
		})
	}
}

func TestOffboardingIsRefusedWithoutAResolver(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	h := newHarnessWith(t, s, tui.Deps{Providers: f.registry(), UnsealMethod: fastPassphrase()})

	h.press("j", "d")

	h.assertShows("Offboarding is refused", "unknown")
	h.assertHides("Offboard work.test from")
}

func TestAZoneTheDelegationCameToNameSinceTheDialogOpenedIsNotOffboarded(t *testing.T) {
	// The Live Zone is checked again just before the delete: the
	// Delegation is resolved anew, not taken from the session.
	s := offboardStore(t)
	f := offboardFake()
	r := offboardResolver()
	h := newOffboardHarness(t, s, f, r)

	h.press("j", "d")
	h.unsealWith(passphrase)
	h.assertShows("Offboard work.test from cloudflare.work?")

	r.mu.Lock()
	r.ns["work.test"] = slices.Clone(workTestNS)
	r.mu.Unlock()

	h.press("enter")

	h.assertShows("work.test is the Live Zone")
	h.assertHides("is Offboarded")

	if calls := deleteZoneCalls(f); len(calls) != 0 || !zoneCached(t, s, "work.test") {
		t.Fatalf("DeleteZone calls = %q, want none and work.test cached", calls)
	}

	// The Delegation resolved anew is the session's: the pane marks the
	// Live Zone by it, and the next Offboarding is refused at once.
	if row := h.domainRow("work.test"); !strings.Contains(row, "●") {
		t.Errorf("work.test row = %q, want it marked the Live Zone", row)
	}

	h.press("d")
	h.assertShows("work.test is the Live Zone")
	h.assertHides("Offboard work.test from")
}

func TestAZoneWhoseDelegationCannotBeResolvedAgainIsNotOffboarded(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	r := offboardResolver()
	h := newOffboardHarness(t, s, f, r)

	h.press("j", "d")
	h.unsealWith(passphrase)

	r.mu.Lock()
	delete(r.ns, "work.test")
	r.mu.Unlock()

	h.press("enter")

	h.assertShows("Offboarding is refused", "unknown")

	if calls := deleteZoneCalls(f); len(calls) != 0 {
		t.Fatalf("DeleteZone calls = %q, want none", calls)
	}
}

func TestAZoneWhoseNameserversAtTheProviderAreDelegatedIsNotOffboarded(t *testing.T) {
	// The cache says other nameservers, but the Provider, read just
	// before the delete, assigns the Zone the delegated ones.
	s := offboardStore(t)
	expectNameservers(t, s, "cloudflare.work", "work.test", "old1.ns.cloudflare.com", "old2.ns.cloudflare.com")

	f := offboardFake()
	r := offboardResolver()
	r.ns["work.test"] = slices.Clone(workTestNS)

	h := newOffboardHarness(t, s, f, r)

	h.press("j", "d")
	h.unsealWith(passphrase)
	h.press("enter")

	h.assertShows("work.test is the Live Zone")

	if calls := deleteZoneCalls(f); len(calls) != 0 {
		t.Fatalf("DeleteZone calls = %q, want none", calls)
	}
}

func TestAZoneSharingANameserverWithTheDelegationIsNotOffboarded(t *testing.T) {
	// Not the Live Zone by the sets, but its Provider may still answer for
	// the name through the nameserver they share.
	s := offboardStore(t)
	r := offboardResolver()
	r.ns["work.test"] = []string{"cruz.ns.cloudflare.com", "ns1.registrar.example"}

	f := offboardFake()
	h := newOffboardHarness(t, s, f, r)

	h.press("j", "d")

	h.assertShows("Offboarding is refused", "cruz.ns.cloudflare.com")
	h.assertHides("Unseal", "Offboard work.test from")
}

func TestOffboardingIsRefusedWithoutUnsealForAProviderTypeThatCannot(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	f.SetZoneDeleter(false)

	h := newOffboardHarness(t, s, f, offboardResolver())

	h.press("j", "d")

	h.assertShows("cloudflare.work cannot Offboard a Zone")
	h.assertHides("Unseal", "Offboard work.test from")
}

func TestOffboardingIsRefusedForAProviderThatCannotThoughItsTypeDeclaresIt(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	f.SetZoneDeleter(false)

	h := newHarnessWith(t, s, tui.Deps{
		Providers: f.declaring(dnsprovider.CapZoneDeleter), UnsealMethod: fastPassphrase(), Resolver: offboardResolver(),
	})

	h.press("j", "d")
	h.unsealWith(passphrase)

	h.assertShows("cloudflare.work cannot Offboard a Zone")
	h.assertHides("Offboard work.test from")
}

func TestGoDaddyAndNamecheapNeverOfferOffboarding(t *testing.T) {
	for _, typ := range []string{"godaddy", "namecheap"} {
		t.Run(typ, func(t *testing.T) {
			s := offboardStore(t)
			a := mustAccount(t, s, typ, "home")
			mustZone(t, s, dns.Zone{AccountID: a.ID, Name: "home.example", ProviderID: "h1", Nameservers: []string{"ns1.home.example", "ns2.home.example"}})

			r := offboardResolver()
			r.ns["home.example"] = slices.Clone(registrarNS)

			// Only cloudflare is registered: the others declare no Zone writes.
			h := newOffboardHarness(t, s, offboardFake(), r)

			// Show the Account's Zones, then Offboard from the Domains pane.
			h.press("shift+tab", "j", "enter", "tab", "?")
			h.assertHides("Offboard the Zone")

			h.press("esc", "d")
			h.assertShows(typ + ".home cannot Offboard a Zone")
			h.assertHides("Unseal", "Offboard home.example from")
		})
	}
}

func TestHelpOffersOffboardingOnlyForACapableAccountsZoneNotLiveInAnUnsealedSession(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	h := newOffboardHarness(t, s, f, offboardResolver())

	// Sealed, it is not offered.
	h.press("j", "?")
	h.assertShows("show the Zone's Records")
	h.assertHides("Offboard the Zone")

	// Unsealed, for work.test, which is not Live.
	h.press("esc", "d")
	h.unsealWith(passphrase)
	h.press("esc", "?")
	h.assertShows("Offboard the Zone")

	// Not for work.example, the Live Zone.
	h.press("esc", "k", "?")
	h.assertHides("Offboard the Zone")
}

func TestAnOffboardingTheProviderFailsKeepsTheDialogOpenWithTheErrorAndTheCache(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	f.Fail(providertest.OpDeleteZone, &dnsprovider.APIError{
		Provider: "cloudflare", StatusCode: 403, Messages: []string{"Unauthorized to access requested resource (code 9109)"},
	})

	h := newOffboardHarness(t, s, f, offboardResolver())

	h.press("j", "d")
	h.unsealWith(passphrase)
	h.press("enter")

	h.assertShows("Offboard work.test from cloudflare.work?", "Could not Offboard work.test", "Unauthorized")
	h.assertHides("is Offboarded")

	if !zoneCached(t, s, "work.test") {
		t.Fatal("work.test left the cache")
	}
}

// lostDelete Offboards through the fake, if take is set, then fails as a
// connection lost before the answer: the delete may have been taken.
type lostDelete struct {
	dnsprovider.Provider
	take bool
	// err, when set, is the failure instead of a lost connection.
	err error
	// ctx is the context the last delete was given.
	ctx *context.Context
}

func (l lostDelete) DeleteZone(ctx context.Context, z dnsprovider.Zone) error {
	if l.ctx != nil {
		*l.ctx = ctx
	}

	if l.take {
		if err := l.Provider.(dnsprovider.ZoneDeleter).DeleteZone(ctx, z); err != nil {
			return err
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if l.err != nil {
		return l.err
	}

	return errConnectionReset
}

func newLostDeleteHarness(t *testing.T, s *store.Store, f *fakeProvider, l lostDelete) *harness {
	t.Helper()

	return newHarnessWith(t, s, tui.Deps{
		Providers: wrapped(f, func(p dnsprovider.Provider) dnsprovider.Provider {
			l.Provider = p

			return l
		}),
		UnsealMethod: fastPassphrase(),
		Resolver:     offboardResolver(),
	})
}

func TestADeleteWhoseAnswerWasLostIsReadBackAndAZoneGoneIsOffboarded(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	h := newLostDeleteHarness(t, s, f, lostDelete{take: true})

	h.press("j", "d")
	h.unsealWith(passphrase)
	h.press("enter")

	h.assertShows("work.test is Offboarded from cloudflare.work")

	if zoneCached(t, s, "work.test") {
		t.Fatal("work.test is still cached")
	}
}

func TestADeleteWhoseAnswerWasLostAndWhoseZoneIsStillThereFails(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	h := newLostDeleteHarness(t, s, f, lostDelete{})

	h.press("j", "d")
	h.unsealWith(passphrase)
	h.press("enter")

	h.assertShows("Could not Offboard work.test", "connection", "cloudflare.work still")
	h.assertHides("is Offboarded")

	if !zoneCached(t, s, "work.test") {
		t.Fatal("work.test left the cache")
	}
}

func TestADeleteRunsWithADeadline(t *testing.T) {
	s := offboardStore(t)

	var ctx context.Context

	h := newLostDeleteHarness(t, s, offboardFake(), lostDelete{take: true, ctx: &ctx})

	h.press("j", "d")
	h.unsealWith(passphrase)
	h.press("enter")

	if ctx == nil {
		t.Fatal("DeleteZone was not called")
	}

	if _, ok := ctx.Deadline(); !ok {
		t.Error("DeleteZone ran without a deadline")
	}
}

func TestEscCancelsAnOffboardingInFlightWhichIsReadBack(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	h := newLostDeleteHarness(t, s, f, lostDelete{})

	h.press("j", "d")
	h.unsealWith(passphrase)

	del := h.pressHolding("enter")
	h.assertShows("offboarding…", "cancels")

	h.press("esc")
	h.run(del)

	h.assertShows("Offboard work.test from cloudflare.work?", "Cancelled Offboarding work.test", "cloudflare.work still")
	h.assertHides("is Offboarded", "offboarding…")

	if !zoneCached(t, s, "work.test") {
		t.Fatal("work.test left the cache")
	}
}

func TestAZoneTheProviderNoLongerHasIsDroppedFromTheCacheWithoutADelete(t *testing.T) {
	s := offboardStore(t)
	f := offboardFake()
	h := newOffboardHarness(t, s, f, offboardResolver())

	h.press("j", "d")
	h.unsealWith(passphrase)

	// Offboarded elsewhere meanwhile.
	f.RemoveZone("zw2")

	h.press("enter")

	h.assertShows("work.test is no longer at cloudflare.work")

	if calls := deleteZoneCalls(f); len(calls) != 0 {
		t.Fatalf("DeleteZone calls = %q, want none", calls)
	}

	if zoneCached(t, s, "work.test") {
		t.Fatal("work.test is still cached")
	}
}

func TestAZoneItsProviderServesOrWhoseHostingStateIsUnknownIsNotOffboarded(t *testing.T) {
	// Not Live by its nameservers, but a Zone its Provider serves is
	// reached by the Delegation all the same: through custom nameservers,
	// a partial setup, or resolvers still caching the old Delegation.
	cases := map[string]struct {
		store func(*testing.T) *store.Store
		want  string
	}{
		"active": {
			store: func(t *testing.T) *store.Store {
				s := offboardStore(t)
				setHostingState(t, s, "work.test", dnsprovider.HostingActive)

				return s
			},
			want: "Offboarding is refused: cloudflare.work serves work.test (active)",
		},
		"unknown": {
			// Never listed with its Hosting State.
			store: func(t *testing.T) *store.Store {
				s, _ := openStore(t)
				a := mustAccount(t, s, "cloudflare", "work")
				mustZone(t, s, dns.Zone{AccountID: a.ID, Name: "work.example", ProviderID: "zw1", Nameservers: workExampleNS})
				mustZone(t, s, dns.Zone{AccountID: a.ID, Name: "work.test", ProviderID: "zw2", Nameservers: workTestNS})

				return s
			},
			want: "Offboarding is refused while the Hosting State of work.test at cloudflare.work is unknown",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := offboardFake()
			h := newOffboardHarness(t, tc.store(t), f, offboardResolver())

			h.press("j", "d")

			h.assertShows(tc.want)
			h.assertHides("Unseal", "Offboard work.test from")
		})
	}
}

func TestAZoneItsProviderServesByNowIsNotOffboarded(t *testing.T) {
	// The cache says pending, but the Provider, read just before the
	// delete, serves it, or no longer says.
	for _, state := range []dnsprovider.HostingState{dnsprovider.HostingActive, dnsprovider.HostingUnknown} {
		t.Run(fmt.Sprint(state), func(t *testing.T) {
			s := offboardStore(t)
			f := offboardFake()
			h := newOffboardHarness(t, s, f, offboardResolver())

			h.press("j", "d")
			h.unsealWith(passphrase)
			h.assertShows("Offboard work.test from cloudflare.work?")

			f.SetHostingState("zw2", state)
			h.press("enter")

			h.assertShows("Offboarding is refused", "work.test")
			h.assertHides("is Offboarded")

			if calls := deleteZoneCalls(f); len(calls) != 0 || !zoneCached(t, s, "work.test") {
				t.Fatalf("DeleteZone calls = %q, want none and work.test cached", calls)
			}
		})
	}
}

func TestAMovedZoneIsOffboarded(t *testing.T) {
	s := offboardStore(t)
	setHostingState(t, s, "work.test", dnsprovider.HostingMoved)

	f := offboardFake()
	f.SetHostingState("zw2", dnsprovider.HostingMoved)

	h := newOffboardHarness(t, s, f, offboardResolver())

	h.press("j", "d")
	h.unsealWith(passphrase)
	h.assertShows("Offboard work.test from cloudflare.work?", "moved")
	h.press("enter")

	h.assertShows("work.test is Offboarded from cloudflare.work")

	if calls := deleteZoneCalls(f); !slices.Equal(calls, []string{"zw2"}) {
		t.Fatalf("DeleteZone calls = %q, want zw2 once", calls)
	}
}

func TestADeleteTheProviderRefusedIsReadBackAndAZoneGoneIsDroppedFromTheCache(t *testing.T) {
	// Whatever the answer, once the delete was sent the Zone is read
	// back: the module retries, and an earlier try may have taken it.
	s := offboardStore(t)
	f := offboardFake()
	h := newLostDeleteHarness(t, s, f, lostDelete{take: true, err: &dnsprovider.APIError{
		Provider: "cloudflare", StatusCode: 400, Messages: []string{"Bad request (code 1000)"},
	}})

	h.press("j", "d")
	h.unsealWith(passphrase)
	h.press("enter")

	h.assertShows("work.test is no longer at cloudflare.work")
	h.assertHides("Offboard work.test from")

	if zoneCached(t, s, "work.test") {
		t.Fatal("work.test is still cached")
	}
}
