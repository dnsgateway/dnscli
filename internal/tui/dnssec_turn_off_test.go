package tui_test

import (
	"errors"
	"testing"

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
