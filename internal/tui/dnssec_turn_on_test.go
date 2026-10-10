package tui_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// turnOnRecords are example.net's Records: the largest TTL, an hour, is
// how long resolvers may still hold its unsigned data once it is signed.
var turnOnRecords = []dns.Record{
	{ProviderID: "rn1", Type: "A", Name: rfcZone, Value: "192.0.2.53", TTL: 300},
	{ProviderID: "rn2", Type: "TXT", Name: rfcZone, Value: "v=spf1 -all", TTL: 3600},
}

// turnOnFake is a Provider hosting example.net as zd1, with its Records,
// its DNSSEC as reported, and able to change it.
func turnOnFake(dnssec dnsprovider.Setting) *fakeProvider {
	f := newFake().AddZone(dns.Zone{Name: rfcZone, ProviderID: "zd1", Nameservers: cruzDee}, turnOnRecords...)
	f.SetZoneStatus("zd1", dnsprovider.ZoneStatus{DNSSEC: dnssec})
	f.SetZoneUpdater(true)

	return f
}

// seenSigned caches that a Zone was first seen signed at at, whenever
// it was seen so before.
func seenSigned(t *testing.T, s *store.Store, name string, at time.Time) {
	t.Helper()

	z := zoneNamed(t, s, name)

	if _, err := s.SaveSigningSeen(context.Background(), z, false, at); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SaveSigningSeen(context.Background(), z, true, at); err != nil {
		t.Fatal(err)
	}
}

// turnOnClock is a Clock a test moves on.
type turnOnClock struct{ now time.Time }

func (c *turnOnClock) Now() time.Time { return c.now }

// newTurnOnHarness starts an unsealed session with cloudflare.work at f,
// which hosts example.net as the Live Zone on cruzDee, with r as its
// resolver and c as its Clock, and opens the Zone, Refreshing it.
func newTurnOnHarness(t *testing.T, f *fakeProvider, r *fakeResolver, c *turnOnClock) (*harness, *store.Store) {
	t.Helper()

	if r.ns == nil {
		r.ns = map[string][]string{}
	}

	r.ns[rfcZone] = cruzDee

	s, _ := openStore(t)
	h := newHarnessWith(t, s, tui.Deps{Providers: f.registry(), UnsealMethod: fastPassphrase(), Resolver: r, Clock: c.Now})
	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")
	h.assertShows(rfcZone, "unsealed")

	h.press("space")
	h.assertShows("validated this session")

	return h, s
}

// restart starts a new, sealed session on s, as after quitting dnscli,
// and opens example.net from the cache, declining to Unseal.
func restart(t *testing.T, s *store.Store, f *fakeProvider, r *fakeResolver, c *turnOnClock) *harness {
	t.Helper()

	h := newHarnessWith(t, s, tui.Deps{Providers: f.registry(), UnsealMethod: fastPassphrase(), Resolver: r, Clock: c.Now})
	h.press("space", "esc")
	h.assertShows("sealed")

	return h
}

func TestTheDSCanBePublishedOnceTheLargestTTLHasPassedSinceSigningWasFirstSeenEvenAcrossARestart(t *testing.T) {
	c := &turnOnClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone)
	f := turnOnFake(dnsprovider.On)

	h, s := newTurnOnHarness(t, f, r, c)

	// Signed at 12:00, with an hour-long TTL: resolvers may hold the
	// unsigned Records until 13:00.
	h.assertStatusBarShows("DNSSEC: DS can be published after 13:00")
	h.assertHides("publish the DS")

	// Half an hour later, after a restart, the wait still ends at 13:00.
	c.now = testNow.Add(30 * time.Minute)
	h = restart(t, s, f, r, c)
	h.assertStatusBarShows("DNSSEC: DS can be published after 13:00")

	// Once it has passed, the DS is to be published.
	c.now = testNow.Add(time.Hour)
	h = restart(t, s, f, r, c)
	h.assertStatusBarShows("DNSSEC: publish the DS")
}

func TestAZoneSeenUnsignedWaitsAfreshOnceItIsSignedAgain(t *testing.T) {
	c := &turnOnClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone)
	r.setDS(rfcZone)
	f := turnOnFake(dnsprovider.Off)

	_, s := newTurnOnHarness(t, f, r, c)

	// It was signed once, long ago; the next session sees it unsigned.
	seenSigned(t, s, rfcZone, testNow.Add(-24*time.Hour))
	h := restart(t, s, f, r, c)
	h.assertHides("publish the DS", "can be published")

	// Signed again by 12:30: the wait starts then.
	c.now = testNow.Add(30 * time.Minute)
	r.setKeys(rfcZone, rfcKey)
	h = restart(t, s, f, r, c)

	h.assertStatusBarShows("DNSSEC: DS can be published after 13:30")
}

func TestAnUnsignedZoneIsOfferedToTurnDNSSECOnThroughTheConfirmation(t *testing.T) {
	c := &turnOnClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone)
	r.setDS(rfcZone)
	f := turnOnFake(dnsprovider.Off)

	h, _ := newTurnOnHarness(t, f, r, c)
	h.assertStatusBarShows("DNSSEC: off (S turns it on)")

	h.press("S")
	h.assertShows("Turn DNSSEC on for example.net at cloudflare.work?")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone before confirming: %+v", calls)
	}

	h.press("enter")

	calls := updateZoneCalls(f)
	if len(calls) != 1 || calls[0].Zone.ID != "zd1" || calls[0].ZoneUpdate.DNSSEC == nil || !*calls[0].ZoneUpdate.DNSSEC {
		t.Fatalf("UpdateZone calls = %+v, want one turning DNSSEC on for zd1", calls)
	}

	// The Provider signs it over time: its nameservers serve no keys yet.
	h.assertStatusBarShows("DNSSEC: waiting for nameservers to serve the keys")
}

// assertNoTurningOn checks that the Zone page shows DNSSEC off but offers
// no way to turn it on, and that asking anyway changes nothing but says
// why, in the status line.
func assertNoTurningOn(t *testing.T, h *harness, f *fakeProvider, why string) {
	t.Helper()

	h.assertStatusBarShows("DNSSEC: off")
	h.assertHides("turns it on")

	h.press("S")
	h.assertHides("Turn DNSSEC on for")
	h.assertStatusLineShows(why)

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone calls = %+v, want none", calls)
	}
}

func TestASealedSessionSeesDNSSECOffButIsOfferedNoWayToTurnItOn(t *testing.T) {
	c := &turnOnClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone)
	r.setDS(rfcZone)
	f := turnOnFake(dnsprovider.Off)

	_, s := newTurnOnHarness(t, f, r, c)
	h := restart(t, s, f, r, c)

	assertNoTurningOn(t, h, f, "The session is sealed")
}

func TestAStaleZoneSeesDNSSECOffButIsOfferedNoWayToTurnItOn(t *testing.T) {
	c := &turnOnClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone)
	r.setDS(rfcZone)
	f := turnOnFake(dnsprovider.Off)

	h, _ := newTurnOnHarness(t, f, r, c)

	// A Refresh that fails leaves the Zone Stale.
	f.Fail(providertest.OpRecords, errors.New("api.cloudflare.com: i/o timeout"))
	h.press("right", "r")
	h.assertHides("validated this session")

	assertNoTurningOn(t, h, f, "is not validated this session")
}

func TestAProviderThatCannotTurnDNSSECOnIsToldToInItsOwnPanel(t *testing.T) {
	c := &turnOnClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone)
	r.setDS(rfcZone)
	f := turnOnFake(dnsprovider.Off)
	f.SetZoneUpdater(false)

	h, _ := newTurnOnHarness(t, f, r, c)

	h.assertStatusBarShows("DNSSEC: off (turn it on in cloudflare.work's panel)")

	h.press("S")
	h.assertHides("Turn DNSSEC on for")
	h.assertStatusLineShows("turn it on in its own panel")
}

// passingTime is an After that lets the time it waits for pass at once:
// it moves c on by each wait, as if dnscli had sat idle meanwhile, after
// calling idle, when set, as dnscli shows it before then.
func passingTime(c *turnOnClock, idle func()) func(time.Duration) <-chan time.Time {
	return func(d time.Duration) <-chan time.Time {
		if idle != nil {
			idle()
		}

		c.now = c.now.Add(d)

		ch := make(chan time.Time, 1)
		ch <- c.now

		return ch
	}
}

// dnssecStates collects each DNSSEC state the Zone Status bar shows, in
// order, as the background DNS queries see it.
type dnssecStates struct {
	h    *harness
	seen []string
}

func (d *dnssecStates) look() {
	if d.h == nil {
		return
	}

	bar := d.h.statusBar()

	i := strings.Index(bar, "DNSSEC: ")
	if i < 0 {
		return
	}

	state, _, _ := strings.Cut(bar[i:], " · ")
	if len(d.seen) == 0 || d.seen[len(d.seen)-1] != state {
		d.seen = append(d.seen, state)
	}
}

func TestTurningDNSSECOnStepsThroughEachStateToOnWithoutARefresh(t *testing.T) {
	c := &turnOnClock{now: testNow}
	r := &fakeResolver{ns: map[string][]string{rfcZone: cruzDee}}
	r.setKeys(rfcZone)
	r.setDS(rfcZone)
	f := turnOnFake(dnsprovider.Off)

	// While the DS is to be published, it is pasted at the registrar by
	// hand.
	states := &dnssecStates{}
	idle := func() {
		states.look()

		if slices.ContainsFunc(states.seen, func(s string) bool { return strings.HasPrefix(s, "DNSSEC: publish the DS") }) {
			r.setDS(rfcZone, rfcDS)
		}
	}

	s, _ := openStore(t)
	h := newHarnessWith(t, s, tui.Deps{
		Providers: f.registry(), UnsealMethod: fastPassphrase(), Resolver: r, Clock: c.Now, After: passingTime(c, idle),
	})
	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")
	h.press("space")
	h.assertStatusBarShows("DNSSEC: off (S turns it on)")

	// The nameservers serve the key from the second time they are asked
	// after signing is turned on.
	states.h = h
	states.look()

	keysAsked := 0
	r.asked = func(query string) {
		if query == "DNSKEY" {
			if keysAsked++; keysAsked == 2 {
				r.setKeys(rfcZone, rfcKey)
			}
		}
	}

	h.press("S", "enter")

	h.assertStatusBarShows("DNSSEC: On")
	states.look()

	want := []string{
		"DNSSEC: off (S turns it on)",
		"DNSSEC: waiting for nameservers to serve the keys",
		// Turned on at 12:00, the key is first served at the re-check a
		// minute later; the largest TTL is an hour.
		"DNSSEC: DS can be published after 13:01",
		"DNSSEC: publish the DS (s shows it)",
		"DNSSEC: On",
	}
	if !slices.Equal(states.seen, want) {
		t.Fatalf("DNSSEC states = %q, want %q", states.seen, want)
	}
}

func TestTheParentIsReCheckedForTheDSForAboutAnHourThenUntilTheNextRefresh(t *testing.T) {
	c := &turnOnClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone)
	f := turnOnFake(dnsprovider.On)

	_, s := newTurnOnHarness(t, f, r, c)
	seenSigned(t, s, rfcZone, testNow.Add(-2*time.Hour))

	h := restart(t, s, f, r, c)
	h.assertStatusBarShows("DNSSEC: publish the DS")

	if got := r.dsQueries(); len(got) < 2 {
		t.Fatalf("DS queries = %v, want the parent asked again", got)
	}

	want := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute}
	if !slices.Equal(h.waits, want) {
		t.Fatalf("waits = %v, want the re-checks %v", h.waits, want)
	}
}
