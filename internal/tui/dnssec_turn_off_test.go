package tui_test

import (
	"errors"
	"testing"
	"time"

	"github.com/dnsgateway/dnsprovider"
)

// pickDNSSECOff opens the Edit Zone dialog of the highlighted Zone, picks
// DNSSEC off and presses Save.
func pickDNSSECOff(h *harness) {
	h.t.Helper()

	h.press("e")
	h.assertShows("‹ on ›")
	h.press("right")
	h.press(saveFromDNSSEC...)
}

func TestUnsigningIsRefusedWhileTheParentServesADS(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone, rfcDS)
	f := turnOnFake(dnsprovider.On)

	h, _ := newTurnOnHarness(t, f, r, c)
	h.assertStatusBarShows("DNSSEC: On")

	pickDNSSECOff(h)
	h.assertShows("DNSSEC stays on", "dnscli watches the parent zone")
	h.assertHides("Turn DNSSEC off for example.net at")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone calls = %+v, want none while the DS is served", calls)
	}

	// The Zone page then says what to do first.
	h.press("esc")
	h.assertStatusBarShows("DNSSEC: remove the DS at the registrar to turn it off")
}

func TestOnceTheDSIsGoneUnsigningWaitsForItsTTLThenIsOfferedAndSent(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone, rfcDS)
	f := turnOnFake(dnsprovider.On)

	h, _ := newTurnOnHarness(t, f, r, c)
	pickDNSSECOff(h)
	h.press("esc")
	h.assertStatusBarShows("DNSSEC: remove the DS at the registrar to turn it off")

	// The DS is removed at the registrar by hand, and seen gone at the
	// re-check a minute later; it was served with an hour-long TTL.
	r.setDS(rfcZone)
	h.passTime(time.Minute)
	h.assertStatusBarShows("DNSSEC: unsign after 13:01")

	// Unsigning before then is still refused.
	pickDNSSECOff(h)
	h.assertShows("DNSSEC stays on until 13:01")
	h.assertHides("Turn DNSSEC off for example.net at")
	h.press("esc")

	h.press("S")
	h.assertHides("Turn DNSSEC off for example.net at")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone calls = %+v, want none before the DS TTL passed", calls)
	}

	// Once it has passed, unsigning is offered, through the confirmation.
	h.passTime(time.Hour)
	h.assertStatusBarShows("DNSSEC: DS gone, ready to unsign (S turns it off)")

	h.press("S")
	h.assertShows("Turn DNSSEC off for example.net at cloudflare.work?")
	h.assertHides("Remove the DS record")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone before confirming: %+v", calls)
	}

	h.press("enter")

	calls := updateZoneCalls(f)
	if len(calls) != 1 || calls[0].Zone.ID != "zd1" || calls[0].ZoneUpdate.DNSSEC == nil || *calls[0].ZoneUpdate.DNSSEC {
		t.Fatalf("UpdateZone calls = %+v, want one turning DNSSEC off for zd1", calls)
	}
}

func TestAFailedDSCheckBlocksUnsigning(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.failDS(rfcZone, errors.New("i/o timeout"))
	f := turnOnFake(dnsprovider.On)

	h, _ := newTurnOnHarness(t, f, r, c)

	// Unknown never counts as gone.
	pickDNSSECOff(h)
	h.assertShows("DNSSEC stays on: dnscli could not tell whether", "only once it sees none")
	h.assertHides("Turn DNSSEC off for example.net at")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone calls = %+v, want none while the DS is unknown", calls)
	}

	h.press("esc")
	h.assertStatusBarShows("DNSSEC: unknown")
}
