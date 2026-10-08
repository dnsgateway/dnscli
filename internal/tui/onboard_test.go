package tui_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"
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
