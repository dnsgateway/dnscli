package tui_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/store"
)

// onboardFake is workZones as a ZoneCreator, whose new Zones are assigned
// two Cloudflare nameservers.
func onboardFake() *fakeProvider {
	f := workZones()
	f.SetZoneCreator(true)
	f.SetNameservers("ada.ns.cloudflare.com", "bob.ns.cloudflare.com")

	return f
}

// createZoneCalls are the zone names the fake was asked to Onboard.
func createZoneCalls(f *fakeProvider) []string {
	var out []string

	for _, c := range f.Calls() {
		if c.Op == providertest.OpCreateZone {
			out = append(out, c.Zone.Name)
		}
	}

	return out
}

func TestOnboardingAZoneNameAsksToConfirmThenCachesItPendingWithItsNameservers(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	h := newAccountHarness(t, s, f)

	// Onboarding is a Zone write: the sealed session Unseals first.
	h.press("i")

	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("the Provider was called while sealed: %+v", calls)
	}

	h.unsealWith(passphrase)
	h.assertShows("Onboard a zone at cloudflare.work", "Zone name")

	h.typeText("new.example")
	h.press("enter")

	h.assertShows("Onboard new.example at cloudflare.work?", "does not register", "registration")

	if calls := createZoneCalls(f); len(calls) != 0 {
		t.Fatalf("CreateZone before confirming: %q", calls)
	}

	h.press("enter")

	if calls := createZoneCalls(f); !slices.Equal(calls, []string{"new.example"}) {
		t.Fatalf("CreateZone calls = %q, want new.example once", calls)
	}

	// The new Zone waits for Delegation to name its nameservers.
	h.assertShows("new.example is Onboarded at cloudflare.work", "pending", "ada.ns.cloudflare.com", "bob.ns.cloudflare.com")

	z := zoneNamed(t, s, "new.example")
	if z.State != dnsprovider.HostingPending || z.ProviderID == "" ||
		!slices.Equal(z.Nameservers, []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}) {
		t.Fatalf("cached Zone = %+v, want it pending with its ID and nameservers", z)
	}

	h.press("esc")
	h.assertHides("Onboard a zone at", "bob.ns.cloudflare.com")
	h.assertShows("new.example · pending")
}

func TestAZoneNameIsCheckedBeforeTheProviderIsAsked(t *testing.T) {
	cases := map[string]string{
		"":                                   "Enter a zone name",
		"example":                            "a domain such as example.com",
		"bad name.example":                   "letters, digits and hyphens",
		"-bad.example":                       "letters, digits and hyphens",
		"bad..example":                       "letters, digits and hyphens",
		"bücher.example":                     "punycode",
		"Work.Example.":                      "cloudflare.work has work.example already",
		strings.Repeat("a", 64) + ".example": "63",
	}

	for typed, want := range cases {
		t.Run(typed, func(t *testing.T) {
			s, _ := enrollFirstAccount(t)
			f := onboardFake()
			h := newAccountHarness(t, s, f)

			h.press("i")
			h.unsealWith(passphrase)
			h.typeText(typed)
			h.press("enter")

			h.assertShows("Onboard a zone at cloudflare.work", want)
			h.assertHides("at cloudflare.work?")

			if calls := createZoneCalls(f); len(calls) != 0 {
				t.Fatalf("CreateZone calls = %q, want none", calls)
			}
		})
	}
}

// zonesCached is how many Zones the cache holds.
func zonesCached(t *testing.T, s *store.Store) int {
	t.Helper()

	zones, err := s.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	return len(zones)
}

func TestAZoneNameTheProviderHoldsAlreadyKeepsTheDialogOpenWithTheErrorAndTheCache(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	f.Fail(providertest.OpCreateZone, &dnsprovider.APIError{
		Provider: "cloudflare", StatusCode: 400, Messages: []string{"taken.example already exists (code 1061)"}, Kind: dnsprovider.ErrConflict,
	})

	h := newAccountHarness(t, s, f)
	before := zonesCached(t, s)

	h.press("i")
	h.unsealWith(passphrase)
	h.typeText("taken.example")
	h.press("enter", "enter")

	h.assertShows("Onboard a zone at cloudflare.work", "Could not Onboard taken.example", "already exists",
		"the Provider holds it", "in this account or another")
	h.assertHides("is Onboarded")

	if got := zonesCached(t, s); got != before {
		t.Fatalf("cached Zones = %d, want %d as before", got, before)
	}

	// The name can be corrected and confirmed again.
	f.Fail(providertest.OpCreateZone, nil)
	h.press("enter", "enter")
	h.assertShows("taken.example is Onboarded at cloudflare.work")
}

func TestEscGoesBackFromConfirmingAnOnboardingThenClosesTheDialogOnboardingNothing(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	h := newAccountHarness(t, s, f)

	h.press("i")
	h.unsealWith(passphrase)
	h.typeText("new.example")
	h.press("enter")
	h.assertShows("Onboard new.example at cloudflare.work?")

	h.press("esc")
	h.assertShows("Onboard a zone at cloudflare.work", "new.example")
	h.assertHides("at cloudflare.work?")

	h.press("esc")
	h.assertHides("Onboard a zone at")

	if calls := createZoneCalls(f); len(calls) != 0 {
		t.Fatalf("CreateZone calls = %q, want none", calls)
	}
}

func TestOnboardingIsRefusedWithoutUnsealForAProviderTypeThatCannot(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	mustAccount(t, s, "namecheap", "home")

	h := newAccountHarness(t, s, onboardFake())

	// Show namecheap.home's Zones, then Onboard from the Domains pane.
	h.press("shift+tab", "j", "enter", "tab", "i")

	h.assertShows("namecheap.home cannot Onboard a zone")
	h.assertHides("Unseal", "Onboard a zone at")
}

func TestOnboardingIsRefusedForAProviderThatCannot(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	f.SetZoneCreator(false)

	h := newAccountHarness(t, s, f)

	h.press("i")
	h.unsealWith(passphrase)

	h.assertShows("cloudflare.work cannot Onboard a zone")
	h.assertHides("Onboard a zone at")
}

func TestOnboardingWithSeveralAccountsActsOnTheAccountShown(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	other := mustAccount(t, s, "cloudflare", "zother")

	f := onboardFake()
	h := newAccountHarness(t, s, f)

	// Zones of every Account are shown: which one is not known.
	h.press("i")
	h.assertShows("Show one Account's Zones first")
	h.assertHides("Unseal", "Onboard a zone at")

	// cloudflare.work's Zones shown, it is the one.
	h.press("shift+tab", "enter", "tab", "i")
	h.unsealWith(passphrase)
	h.assertShows("Onboard a zone at cloudflare.work")
	h.assertHides("Onboard a zone at " + other.Label())
}

func TestOnboardingIsRefusedForAnAccountActingWithinNoTenant(t *testing.T) {
	f := refreshZones()
	f.SetZoneCreator(true)
	f.SetTenants() // the token reaches none it may list: unscoped

	h, _ := newUpgradedHarness(t, f)

	// Show cloudflare.old's Zones, and Onboard in it.
	h.press("enter", "tab", "i")
	h.unsealWith(passphrase)

	h.assertShows("cloudflare.old acts within no single Provider account")
	h.assertHides("Onboard a zone at")

	if calls := createZoneCalls(f); len(calls) != 0 {
		t.Fatalf("CreateZone calls = %q, want none", calls)
	}
}

func TestOnboardingInAnAccountReachingSeveralTenantsAsksForOneThenOpens(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)
	f.SetZoneCreator(true)

	h, _ := newUpgradedHarness(t, f)

	h.press("enter", "tab", "i")
	h.unsealWith(passphrase)

	h.assertShows("Tenant of cloudflare.old")
	h.assertHides("Onboard a zone at")
	onlyTenantsListed(t, f)

	h.press("enter")
	h.assertShows("Onboard a zone at cloudflare.old")
}

func TestAMovedZonesNameIsOnboardedAgainInItsPlace(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	moved := zoneNamed(t, s, "work.test")
	moved.State = dnsprovider.HostingMoved
	mustZone(t, s, moved)

	f := onboardFake()
	f.SetHostingState("zw2", dnsprovider.HostingMoved)

	h := newAccountHarness(t, s, f)

	h.press("i")
	h.unsealWith(passphrase)
	h.typeText("work.test")
	h.press("enter", "enter")

	h.assertShows("work.test is Onboarded at cloudflare.work")

	z := zoneNamed(t, s, "work.test")
	if z.ID != moved.ID || z.ProviderID == "zw2" || z.State != dnsprovider.HostingPending {
		t.Fatalf("cached Zone = %+v, want the Moved one replaced by a pending one", z)
	}
}

func TestHelpOffersOnboardingInTheDomainsPane(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newAccountHarness(t, s, workZones())

	h.press("?")
	h.assertShows("Onboard a zone name")
}

func TestAZoneOnboardedButNotCachedClosesTheDialogNamingIt(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	h := newAccountHarness(t, s, f)

	h.press("i")
	h.unsealWith(passphrase)
	h.typeText("new.example")
	h.press("enter")

	// The Account is removed meanwhile, as by another dnscli.
	if err := s.RemoveAccount(context.Background(), accountLabeled(t, s, "cloudflare.work").ID); err != nil {
		t.Fatal(err)
	}

	h.press("enter")

	if calls := createZoneCalls(f); len(calls) != 1 {
		t.Fatalf("CreateZone calls = %q, want one", calls)
	}

	h.assertShows("new.example was Onboarded at cloudflare.work, but could not be cached")
	h.assertHides("Onboard a zone at", "onboarding…")
}
