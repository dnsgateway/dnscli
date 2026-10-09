package tui_test

import (
	"fmt"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"
)

// registrationOnlyText is how a Zone whose Provider serves no DNS Records for
// it, such as a domain an Account only registers, is described.
const registrationOnlyText = "registration only"

// A registrar answers a Refresh of a domain it does not serve DNS for, such
// as one delegated elsewhere, with ErrNotServed: the Zone is marked
// registration only, not as a failed Refresh, and its edits are refused,
// as they would not apply.
func TestAZoneItsProviderServesNoRecordsForIsMarkedRegistrationOnly(t *testing.T) {
	f := refreshZones()
	h, _, _ := newRefreshHarness(t, f)

	f.Fail(providertest.OpRecords, fmt.Errorf("namecheap: domain is not using proper DNS servers: %w", dnsprovider.ErrNotServed))
	h.press("j", "enter")

	h.assertShows(registrationOnlyText, "serves no DNS Records for work.test")
	h.assertHides("Refresh failed", "No cached Records")
	h.assertMarker("work.test", "⌀")

	h.press("tab", "i")
	h.assertShows("work.test is registration only")
	h.assertHides("Add Record", "Stale Zone")

	// Once the Provider serves it, a Refresh validates it.
	f.Fail(providertest.OpRecords, nil)
	h.press("r")

	h.assertShows("validated this session", "No cached Records for work.test.")
	h.assertHides(registrationOnlyText)
	h.assertMarker("work.test", "✓")
}

func TestOtherRefreshFailuresAreNotRegistrationOnly(t *testing.T) {
	// A Zone gone from its Provider is not found, not registration only.
	for _, err := range []error{dnsprovider.ErrUnauthorized, fmt.Errorf("cloudflare: zone gone: %w", dnsprovider.ErrNotFound)} {
		f := refreshZones()
		h, _, _ := newRefreshHarness(t, f)

		f.Fail(providertest.OpRecords, err)
		h.press("j", "enter")

		h.assertShows("Refresh failed", "No cached Records for work.test.")
		h.assertHides(registrationOnlyText)
		h.assertMarker("work.test", "✗")
	}
}

// While a registration-only Zone is Refreshed again, it is not said to be
// registration only: the Refresh may find it served.
func TestARegistrationOnlyZoneBeingRefreshedIsNotSaidToBeRegistrationOnly(t *testing.T) {
	f := refreshZones()
	h, _, _ := newRefreshHarness(t, f)

	f.Fail(providertest.OpRecords, dnsprovider.ErrNotServed)
	h.press("j", "enter")
	h.assertMarker("work.test", "⌀")

	h.press("tab")
	refresh := h.pressHolding("r")
	h.press("i")

	h.assertShows("work.test is being Refreshed")
	h.assertMarker("work.test", "↻")
	h.assertHides(registrationOnlyText, "Add Record")

	f.Fail(providertest.OpRecords, nil)
	h.run(refresh)
	h.assertMarker("work.test", "✓")
}
