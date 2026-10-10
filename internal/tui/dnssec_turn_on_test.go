package tui_test

import (
	"context"
	"testing"
	"time"

	"github.com/dnsgateway/dnsprovider"

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

// seenSigned caches that a Zone was first seen signed at at.
func seenSigned(t *testing.T, s *store.Store, name string, at time.Time) {
	t.Helper()

	if _, err := s.SaveSigningSeen(context.Background(), zoneNamed(t, s, name), true, at); err != nil {
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
