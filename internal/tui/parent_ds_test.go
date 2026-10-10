package tui_test

import (
	"errors"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// The DS records example.com's parent serves in these tests.
const (
	dsOne = "2371 13 2 1F987CC6583E92DF0890718C42"
	dsTwo = "60485 13 2 D4B7D520E7BB5F0F67674A0CCEB1E3E0614B93C4F9E99B8383F6A1E4469DA50A"
)

// newDSHarness starts a session on the seeded cache with r as its resolver.
func newDSHarness(t *testing.T, r *fakeResolver) (*harness, *store.Store) {
	t.Helper()

	s, _ := openStore(t)
	seedCache(t, s)

	return newSerialCheckHarness(t, s, r), s
}

func TestOpeningAZoneShowsHowManyDSRecordsItsParentServes(t *testing.T) {
	r := &fakeResolver{}
	r.setDS("example.com", dsOne, dsTwo)
	h, _ := newDSHarness(t, r)

	h.press("space")

	h.assertStatusBarShows("DS at parent: yes (2)")

	if got := r.dsQueries(); len(got) != 1 || got[0] != "example.com" {
		t.Fatalf("DS queries = %v, want one for example.com", got)
	}
}

func TestAParentThatServesNoDSSaysNo(t *testing.T) {
	r := &fakeResolver{}
	r.setDS("example.com")
	h, _ := newDSHarness(t, r)

	h.press("space")

	h.assertStatusBarShows("DS at parent: no")
}

func TestAFailedDSCheckShowsUnknownNeverNo(t *testing.T) {
	r := &fakeResolver{}
	r.failDS("example.com", errors.New("i/o timeout"))
	h, _ := newDSHarness(t, r)

	h.press("space")

	h.assertStatusBarShows("DS at parent: unknown")
	h.assertHides("DS at parent: no")
}

func TestADSNotCheckedThisSessionSaysSo(t *testing.T) {
	// Offline: no resolver, so nothing is checked this session.
	s, _ := openStore(t)
	seedCache(t, s)
	h := newHarness(t, s)

	h.press("space")

	h.assertStatusBarShows("DS at parent: not checked this session")
}

func TestTheDSCheckRunsInTheBackgroundOnceTheZoneIsOpened(t *testing.T) {
	r := &fakeResolver{}
	r.setDS("example.com", dsOne)
	h, _ := newDSHarness(t, r)

	// Opening the Zone shows its cached Records, then checks in the background.
	opened := h.step(h.pressHolding("space"))
	h.assertStatusBarShows("DS at parent: checking…")
	h.assertShows("192.0.2.10")

	h.run(opened)
	h.assertStatusBarShows("DS at parent: yes (1)")
}

func TestDSChecksWaitForAFreeSlotAmongTheDelegationLookups(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	r := &fakeResolver{ns: map[string][]string{}}
	r.setDS("example.com", dsOne)
	h, startup := newHarnessHoldingStartup(t, s, tui.Deps{Resolver: r, SerialChecks: 1})

	// The one slot is taken by a Delegation lookup: opening a Zone queues
	// its DS check behind it.
	lookups := runOnce(startup)
	if got := r.nsQueries(); len(got) != 1 {
		t.Fatalf("NS queries in flight = %v, want 1", got)
	}

	h.press("space")
	h.assertStatusBarShows("DS at parent: checking…")

	if got := r.dsQueries(); len(got) != 0 {
		t.Fatalf("DS queries = %v, want none while the slot is taken", got)
	}

	for _, msg := range lookups {
		h.send(msg)
	}

	if got, ns := r.dsQueries(), r.nsQueries(); len(got) != 1 || len(ns) != 3 {
		t.Fatalf("DS queries = %v, NS queries = %v; want example.com's DS and each zone name's NS asked", got, ns)
	}

	h.assertStatusBarShows("DS at parent: yes (1)")
}

func TestRefreshingTheZoneChecksTheDSAtItsParentAgain(t *testing.T) {
	r := &fakeResolver{}
	r.setDS("example.com")
	h, _ := newDSHarness(t, r)

	// Opening the Zone asks to Unseal, for its Refresh: the user declines.
	h.press("space", "esc")
	h.assertStatusBarShows("DS at parent: no")

	// The DS is published at the registrar meanwhile. A Refresh asks for
	// secrets again, but the DS is public: it is checked even sealed.
	r.setDS("example.com", dsOne)
	h.press("right", "r", "esc")

	h.assertStatusBarShows("DS at parent: yes (1)")

	if got := r.dsQueries(); len(got) != 2 {
		t.Fatalf("DS queries = %v, want example.com asked again", got)
	}
}
