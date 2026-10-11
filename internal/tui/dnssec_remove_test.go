package tui_test

import (
	"errors"
	"testing"
	"time"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"
)

// newRemoveHarness is newTurnOnHarness with example.net signed, its keys
// served and its DS at the parent, once the user asked to turn DNSSEC
// off: the DS is to be removed first.
func newRemoveHarness(t *testing.T, f *fakeProvider, r *fakeResolver, c *testClock) *harness {
	t.Helper()

	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone, rfcDS)

	h, _ := newTurnOnHarness(t, f, r, c)
	pickDNSSECOff(h)
	h.press("esc")
	h.assertStatusBarShows("DNSSEC: remove the DS at the registrar to turn it off")

	return h
}

func TestTheDSIsRemovedAtTheRegistrarThenTheZoneIsUnsignedOnceItsTTLPassed(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1", rfcModuleKey)

	h := newRemoveHarness(t, f, r, c)
	h.assertStatusBarShows("(S removes it)")

	h.press("S")
	h.assertShows("Remove the DS of example.net at cloudflare.work?", rfcDS)

	if calls := setDSCalls(f); len(calls) != 0 {
		t.Fatalf("SetDelegationSigners before confirming: %+v", calls)
	}

	h.press("enter")

	calls := setDSCalls(f)
	if len(calls) != 1 || calls[0].Zone.ID != "zd1" || len(calls[0].Keys) != 0 {
		t.Fatalf("SetDelegationSigners calls = %+v, want one emptying the set of zd1", calls)
	}

	// Removed, it waits for the parent zone to stop serving it.
	h.assertStatusBarShows("DNSSEC: DS removed, waiting for the parent zone")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone calls = %+v, want none while the parent serves the DS", calls)
	}

	// Seen gone at the re-check a minute later; it was served with an
	// hour-long TTL.
	r.setDS(rfcZone)
	h.passTime(time.Minute)
	h.assertStatusBarShows("DNSSEC: unsign after 13:01")

	h.passTime(time.Hour)
	h.assertStatusBarShows("DNSSEC: DS gone, ready to unsign (S turns it off)")

	h.press("S")
	h.assertShows("Turn DNSSEC off for example.net at cloudflare.work?")
	h.press("enter")

	up := updateZoneCalls(f)
	if len(up) != 1 || up[0].Zone.ID != "zd1" || up[0].ZoneUpdate.DNSSEC == nil || *up[0].ZoneUpdate.DNSSEC {
		t.Fatalf("UpdateZone calls = %+v, want one turning DNSSEC off for zd1", up)
	}
}

func TestCancellingTheRemovalWritesNothingAndKeepsTheDSFirst(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1", rfcModuleKey)

	h := newRemoveHarness(t, f, r, c)

	h.press("S")
	h.assertShows("Remove the DS of example.net at cloudflare.work?")

	h.press("esc")
	h.assertHides("Remove the DS of")
	h.assertStatusBarShows("DNSSEC: remove the DS at the registrar to turn it off (S removes it)")

	if calls := setDSCalls(f); len(calls) != 0 {
		t.Fatalf("SetDelegationSigners once canceled: %+v", calls)
	}

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone calls = %+v, want none while the parent serves the DS", calls)
	}
}

func TestWithoutAnAccountThatRegistersTheDomainTheDSIsToBeRemovedByHand(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)

	h := newRemoveHarness(t, f, r, c)

	h.press("S")
	h.assertHides("Remove the DS of")
	h.assertStatusLineShows("No Account in dnscli removes the DS of example.net")
	h.assertStatusBarShows("DNSSEC: remove the DS at the registrar to turn it off")

	// Removed by hand, the turn-off goes on as before.
	r.setDS(rfcZone)
	h.passTime(time.Minute)
	h.assertStatusBarShows("DNSSEC: unsign after 13:01")
}

func TestAStaleZoneIsToldToRemoveTheDSByHandAndAsksNoRegistrar(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1", rfcModuleKey)

	h := newRemoveHarness(t, f, r, c)

	// A Refresh that fails leaves the Zone Stale: no DS write passes the
	// write gate.
	f.Fail(providertest.OpRecords, errors.New("api.cloudflare.com: i/o timeout"))
	h.press("right", "r")
	h.assertHides("validated this session")
	h.assertStatusBarShows("DNSSEC: remove the DS at the registrar to turn it off")
	h.assertHides("S removes it")

	h.press("S")
	h.assertHides("Remove the DS of")
	h.assertStatusLineShows("Remove the DS record for example.net at the registrar first")

	if n := f.count(providertest.OpDelegationSigners); n != 0 {
		t.Fatalf("DelegationSigners asked %d times for a Stale Zone", n)
	}
}

func TestWithoutAnAccountThatRegistersTheDomainAMismatchedDSShowsTheKeysToCopy(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone, dsOne)
	f := turnOnFake(dnsprovider.On)

	h, _ := newTurnOnHarness(t, f, r, c)

	h.press("S")
	h.assertShows("DNSSEC keys for example.net", rfcDS, "No Account in dnscli replaces the DS of example.net")
	h.assertHides("Replace the DS of")
}

func TestAParentDSThatMatchesNoKeyIsReplacedWithTheZonesKeys(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone, dsOne)
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1")

	h, _ := newTurnOnHarness(t, f, r, c)
	h.assertStatusBarShows("DNSSEC: DS does not match (S replaces it, s shows the keys)")

	h.press("S")
	h.assertShows("Replace the DS of example.net at cloudflare.work?", rfcDNSKEY, rfcDS)

	if calls := setDSCalls(f); len(calls) != 0 {
		t.Fatalf("SetDelegationSigners before confirming: %+v", calls)
	}

	h.press("enter")
	assertPublishedOnce(t, f, "zd1")

	// Replaced, it is On only once the parent zone serves the new DS.
	h.assertStatusBarShows("DNSSEC: DS replaced, waiting for the parent zone")

	r.setDS(rfcZone, rfcDS)
	h.passTime(time.Minute)
	h.assertStatusBarShows("DNSSEC: On")
}

func TestCancellingTheReplacementWritesNothingAndKeepsTheMismatch(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone, dsOne)
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1")

	h, _ := newTurnOnHarness(t, f, r, c)

	h.press("S")
	h.assertShows("Replace the DS of example.net at cloudflare.work?")

	h.press("esc")
	h.assertHides("Replace the DS of")
	h.assertStatusBarShows("DNSSEC: DS does not match (S replaces it, s shows the keys)")

	if calls := setDSCalls(f); len(calls) != 0 {
		t.Fatalf("SetDelegationSigners once canceled: %+v", calls)
	}
}

func TestTurningOffAZoneThatServesNoKeysOffersToRemoveTheDS(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone)
	r.setDS(rfcZone, rfcDS)
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1", rfcModuleKey)

	h, _ := newTurnOnHarness(t, f, r, c)
	pickDNSSECOff(h)
	h.press("esc")
	h.assertStatusBarShows("DNSSEC: remove the DS at the registrar to turn it off (S removes it)")

	h.press("S")
	h.assertShows("Remove the DS of example.net at cloudflare.work?", rfcDS)
	h.press("enter")

	calls := setDSCalls(f)
	if len(calls) != 1 || calls[0].Zone.ID != "zd1" || len(calls[0].Keys) != 0 {
		t.Fatalf("SetDelegationSigners calls = %+v, want one emptying the set of zd1", calls)
	}

	h.assertStatusBarShows("DNSSEC: DS removed, waiting for the parent zone")
}

func TestADSForAZoneThatServesNoKeysIsOfferedForRemoval(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone)
	r.setDS(rfcZone, rfcDS)
	f := turnOnFake(dnsprovider.Off)
	f.SetRegistration("zd1", rfcModuleKey)

	h, _ := newTurnOnHarness(t, f, r, c)
	h.assertStatusBarShows("DNSSEC: DS does not match (S removes it)")

	h.press("S")
	h.assertShows("Remove the DS of example.net at cloudflare.work?", rfcDS, "the Zone serves no keys")
	h.assertHides("DNSSEC keys for example.net", "unsigns the Zone")
	h.press("enter")

	calls := setDSCalls(f)
	if len(calls) != 1 || calls[0].Zone.ID != "zd1" || len(calls[0].Keys) != 0 {
		t.Fatalf("SetDelegationSigners calls = %+v, want one emptying the set of zd1", calls)
	}

	h.assertStatusBarShows("DNSSEC: DS removed, waiting for the parent zone")

	r.setDS(rfcZone)
	h.passTime(time.Minute)
	h.assertStatusBarShows("DNSSEC: off")
}

func TestADSWriteIsNotSentOnceItsStepPassedWhileTheConfirmationWasOpen(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1", rfcModuleKey)

	h := newRemoveHarness(t, f, r, c)

	h.press("S")
	h.assertShows("Remove the DS of example.net at cloudflare.work?")

	// Removed by hand meanwhile: the re-check sees it gone.
	r.setDS(rfcZone)
	h.passTime(time.Minute)
	h.press("enter")

	if calls := setDSCalls(f); len(calls) != 0 {
		t.Fatalf("SetDelegationSigners once the DS was gone: %+v", calls)
	}

	h.assertHides("Remove the DS of")
	h.assertStatusLineShows("The DS of example.net was not removed")
	h.assertStatusBarShows("DNSSEC: unsign after 13:01")
}
