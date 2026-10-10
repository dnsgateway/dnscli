package tui_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// onboardFake is workZones as a ZoneCreator, whose new Zones are assigned
// two Cloudflare nameservers.
func onboardFake() *fakeProvider {
	f := workZones()
	f.SetZoneCreator(true)
	f.SetNameservers("ada.ns.cloudflare.com", "bob.ns.cloudflare.com")

	return f
}

// onboardDialog is the Onboard dialog's title, as its border shows it.
const onboardDialog = "━ Onboard a zone ━"

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
	h.assertShows(onboardDialog, "Account", "cloudflare.work", "Zone name")

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
	h.assertHides(onboardDialog, "bob.ns.cloudflare.com")
	h.assertShows("new.example · pending")
}

func TestAZoneNameIsCheckedBeforeTheProviderIsAsked(t *testing.T) {
	cases := map[string]string{
		"":                                    "Enter a zone name",
		"example":                             "a domain such as example.com",
		"bad name.example":                    "letters, digits and hyphens",
		"bad..example":                        "letters, digits and hyphens",
		"bücher.example":                      "punycode",
		"Work.Example.":                       "cloudflare.work has work.example already",
		strings.Repeat("a", 64) + ".example":  "63",
		strings.Repeat("a.", 127) + "example": "253",
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

			h.assertShows(onboardDialog, want)
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

	h.assertShows(onboardDialog, "Could not Onboard taken.example", "already exists",
		"holds no Zone", "another")
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
	h.assertShows(onboardDialog, "new.example")
	h.assertHides("at cloudflare.work?")

	h.press("esc")
	h.assertHides(onboardDialog)

	if calls := createZoneCalls(f); len(calls) != 0 {
		t.Fatalf("CreateZone calls = %q, want none", calls)
	}
}

func TestAnAccountWhoseProviderTypeCannotOnboardIsNotOffered(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	mustAccount(t, s, "namecheap", "home")

	h := newAccountHarness(t, s, onboardFake())

	// Show namecheap.home's Zones, then Onboard from the Domains pane: the
	// only Account that can is chosen, and there is no other to choose.
	h.press("shift+tab", "j", "space", "tab", "i")
	h.unsealWith(passphrase)

	h.assertShows(onboardDialog, "Account    cloudflare.work")

	h.press("up", "right")
	h.assertShows("Account    cloudflare.work")
	h.assertHides("Account    namecheap.home", "‹ cloudflare.work ›")
}

func TestOnboardingIsRefusedWithoutUnsealWhenNoAccountsProviderTypeDeclaresIt(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	f.SetZoneCreator(false)

	h := newAccountHarness(t, s, f)

	h.press("i")

	h.assertShows("No Account can Onboard a zone")
	h.assertHides("Unseal", onboardDialog)
}

func TestOnboardingIsRefusedForAProviderThatCannotThoughItsTypeDeclaresIt(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	f.SetZoneCreator(false)

	h := newHarnessWith(t, s, tui.Deps{Providers: f.declaring(dnsprovider.CapZoneCreator), UnsealMethod: fastPassphrase()})

	h.press("i")
	h.unsealWith(passphrase)
	h.assertShows(onboardDialog, "cloudflare.work cannot Onboard a zone")

	h.typeText("new.example")
	h.press("enter")
	h.assertHides("at cloudflare.work?")
}

func TestOnboardingWithSeveralAccountsChoosesTheOneShownOrHighlightedAndAnyOther(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	other := addOldAccount(t, s, "cloudflare", "zother", "tok")

	f := onboardFake()
	h := newAccountHarness(t, s, f)

	// Zones of every Account are shown: the one highlighted in the
	// Accounts pane is chosen.
	h.press("i")
	h.unsealWith(passphrase)
	h.assertShows(onboardDialog, "Account    cloudflare.work")
	h.press("esc")

	// Highlighted, or shown, zother is.
	h.press("shift+tab", "j", "tab", "i")
	h.assertShows("Account    " + other.Label())
	h.press("esc")

	h.press("shift+tab", "k", "tab", "i")
	h.assertShows("Account    cloudflare.work")

	// Any other is chosen in the dialog, from its list.
	h.press("up", "enter")
	h.assertShows("━ Account ━", other.Label())
	h.press("j", "enter")
	h.assertShows("Account    ‹ " + other.Label() + " ›")

	// Left and Right change it too.
	h.press("left")
	h.assertShows("‹ cloudflare.work ›")
	h.press("right")

	h.press("down")
	h.typeText("new.example")
	h.press("enter")
	h.assertShows("Onboard new.example at " + other.Label() + "?")
	h.press("enter")

	if z := zoneNamed(t, s, "new.example"); z.AccountID != other.ID {
		t.Fatalf("cached Zone = %+v, want it in %s", z, other.Label())
	}
}

func TestOnboardingIsRefusedForAnAccountActingWithinNoTenant(t *testing.T) {
	f := refreshZones()
	f.SetZoneCreator(true)
	f.SetTenants() // the token reaches none it may list: unscoped

	h, _ := newUpgradedHarness(t, f)

	// Show cloudflare.old's Zones, and Onboard in it.
	h.press("space", "tab", "i")
	h.unsealWith(passphrase)

	h.assertShows(onboardDialog, "cloudflare.old acts within no single Provider account")

	h.typeText("new.example")
	h.press("enter")
	h.assertHides("at cloudflare.old?")

	if calls := createZoneCalls(f); len(calls) != 0 {
		t.Fatalf("CreateZone calls = %q, want none", calls)
	}
}

func TestOnboardingInAnAccountReachingSeveralTenantsChoosesOneInTheDialog(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)
	f.SetZoneCreator(true)
	f.SetAccount(dnsprovider.AccountInfo{Tenant: twoTenants[1]})

	h, s := newUpgradedHarness(t, f)

	h.press("space", "tab", "i")
	h.unsealWith(passphrase)

	// The first Tenant is chosen, as the first Record type is.
	h.assertShows(onboardDialog, "Account    cloudflare.old", "Tenant     Acme Ltd", "reach several Tenants")
	onlyTenantsListed(t, f)

	// Left and Right change it, and Enter lists every one.
	h.press("up", "right")
	h.assertShows("‹ Side project ›")
	h.press("left")
	h.assertShows("‹ Acme Ltd ›")
	h.press("enter")
	h.assertShows("Tenant of cloudflare.old", "Acme Ltd", "Side project")
	h.press("j", "enter")
	h.assertShows("‹ Side project ›")
	h.assertHides("Tenant of cloudflare.old")
	onlyTenantsListed(t, f)

	// A name checked asks to confirm within the Tenant chosen, which is
	// saved only once it is: backing out saves nothing.
	h.press("down")
	h.typeText("new.example")
	h.press("enter")
	h.assertShows("Onboard new.example at cloudflare.old (Side project)?", "Side project is saved as the Tenant")
	h.press("esc")
	onlyTenantsListed(t, f)

	h.press("enter", "enter")

	if got := storedFields(t, s, accountLabeled(t, s, "cloudflare.old"))["account"]; got != "b2" {
		t.Fatalf("stored Tenant = %q, want b2", got)
	}

	h.assertShows("new.example is Onboarded at cloudflare.old")
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

func TestHelpOffersOnboardingOnlyForACapableAccountInAnUnsealedSession(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newAccountHarness(t, s, onboardFake())

	// Sealed, it is not offered.
	h.press("?")
	h.assertShows("show the Zone's Records")
	h.assertHides("Onboard a zone name")

	// Unsealed, for the only Account, which can.
	h.press("esc", "i")
	h.unsealWith(passphrase)
	h.press("esc", "?")
	h.assertShows("Onboard a zone name")
}

func TestHelpDoesNotOfferOnboardingForAnAccountThatCannot(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	f.SetZoneCreator(false)
	f.SetZoneUpdater(true)

	h := newAccountHarness(t, s, f)

	// Unseal through the Edit Zone dialog, as it can change Zone settings.
	h.press("e", "enter")
	h.unsealWith(passphrase)
	h.press("esc", "?")

	h.assertShows("show the Zone's Records")
	h.assertHides("Onboard a zone name")
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
	h.assertHides(onboardDialog, "onboarding…")
}

func TestOnboardingIsRefusedForAnAccountTheFakeBuildsWithinNoTenant(t *testing.T) {
	// The module fake, scripted with Tenants, takes its own Tenant field,
	// as Cloudflare does, and Onboards nothing without one.
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	f.SetTenants() // the token reaches none it may list: unscoped

	h := newAccountHarness(t, s, f)

	h.press("i")
	h.unsealWith(passphrase)

	h.assertShows(onboardDialog, "cloudflare.work acts within no single Provider account")

	h.typeText("new.example")
	h.press("enter", "enter")

	if calls := createZoneCalls(f); len(calls) != 0 {
		t.Fatalf("CreateZone calls = %q, want none", calls)
	}
}

// unsaidCreator Onboards through the fake, but answers, as Cloudflare
// may when it took the create without saying what it made, with the
// name alone, Pending, without an ID or nameservers.
type unsaidCreator struct{ dnsprovider.Provider }

func (u unsaidCreator) CreateZone(ctx context.Context, name string) (dnsprovider.Zone, error) {
	z, err := u.Provider.(dnsprovider.ZoneCreator).CreateZone(ctx, name)
	if err != nil {
		return z, err
	}

	return dnsprovider.Zone{Name: z.Name, State: dnsprovider.HostingPending}, nil
}

func TestAZoneOnboardedWithoutAnIDIsNotCachedAndTheUserIsToldToReList(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	h := newHarnessWith(t, s, tui.Deps{
		Providers:    wrapped(f, func(p dnsprovider.Provider) dnsprovider.Provider { return unsaidCreator{p} }),
		UnsealMethod: fastPassphrase(),
	})
	before := zonesCached(t, s)

	h.press("i")
	h.unsealWith(passphrase)
	h.typeText("new.example")
	h.press("enter", "enter")

	h.assertShows("new.example is Onboarded at cloudflare.work", "did not say", "Re-list")
	h.assertHides("onboarding…")

	if got := zonesCached(t, s); got != before {
		t.Fatalf("cached Zones = %d, want %d: a Zone without its Provider ID is not cached", got, before)
	}
}

func TestAConflictOverAZoneNameTheAccountHoldsShowsThatZoneAsOnboarded(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	// Onboarded already, as by a create whose answer was lost: the Account
	// holds it, pending, but the cache does not.
	f.Provider.AddZone(dnsprovider.Zone{
		ID: "zh", Name: "held.example", State: dnsprovider.HostingPending,
		Nameservers: []string{"cy.ns.cloudflare.com", "dee.ns.cloudflare.com"},
	})

	h := newAccountHarness(t, s, f)

	h.press("i")
	h.unsealWith(passphrase)
	h.typeText("held.example")
	h.press("enter", "enter")

	h.assertShows("held.example is Onboarded at cloudflare.work", "pending", "cy.ns.cloudflare.com", "dee.ns.cloudflare.com")
	h.assertHides("Could not Onboard")

	if z := zoneNamed(t, s, "held.example"); z.ProviderID != "zh" || z.State != dnsprovider.HostingPending {
		t.Fatalf("cached Zone = %+v, want the Account's pending zh", z)
	}
}

// lostAnswer Onboards through the fake, unless fail is set, then fails as
// a connection lost before the answer: the create may have been taken.
type lostAnswer struct {
	dnsprovider.Provider
	take bool
	// ctx is the context the last create was given.
	ctx *context.Context
}

var errConnectionReset = errors.New("read tcp: connection reset by peer")

func (l lostAnswer) CreateZone(ctx context.Context, name string) (dnsprovider.Zone, error) {
	if l.ctx != nil {
		*l.ctx = ctx
	}

	if l.take {
		if _, err := l.Provider.(dnsprovider.ZoneCreator).CreateZone(ctx, name); err != nil {
			return dnsprovider.Zone{}, err
		}
	}

	if err := ctx.Err(); err != nil {
		return dnsprovider.Zone{}, err
	}

	return dnsprovider.Zone{}, errConnectionReset
}

func newLostAnswerHarness(t *testing.T, s *store.Store, f *fakeProvider, l lostAnswer) *harness {
	t.Helper()

	return newHarnessWith(t, s, tui.Deps{
		Providers: wrapped(f, func(p dnsprovider.Provider) dnsprovider.Provider {
			l.Provider = p

			return l
		}),
		UnsealMethod: fastPassphrase(),
	})
}

func TestACreateWhoseAnswerWasLostIsReadBackAndShownOnboarded(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	h := newLostAnswerHarness(t, s, f, lostAnswer{take: true})

	h.press("i")
	h.unsealWith(passphrase)
	h.typeText("new.example")
	h.press("enter", "enter")

	h.assertShows("new.example is Onboarded at cloudflare.work", "ada.ns.cloudflare.com")

	if calls := createZoneCalls(f); len(calls) != 1 {
		t.Fatalf("CreateZone calls = %q, want one: a create is never sent again", calls)
	}

	if z := zoneNamed(t, s, "new.example"); z.ProviderID == "" || z.State != dnsprovider.HostingPending {
		t.Fatalf("cached Zone = %+v, want it pending with its ID", z)
	}
}

func TestACreateWhoseAnswerWasLostAndThatTheAccountDoesNotHoldFails(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	h := newLostAnswerHarness(t, s, f, lostAnswer{})
	before := zonesCached(t, s)

	h.press("i")
	h.unsealWith(passphrase)
	h.typeText("new.example")
	h.press("enter", "enter")

	h.assertShows("Could not Onboard new.example", "connection", "holds no Zone", "nothing was Onboarded")
	h.assertHides("is Onboarded")

	if got := zonesCached(t, s); got != before {
		t.Fatalf("cached Zones = %d, want %d as before", got, before)
	}
}

func TestACreateRunsWithADeadline(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	var ctx context.Context

	h := newLostAnswerHarness(t, s, onboardFake(), lostAnswer{take: true, ctx: &ctx})

	h.press("i")
	h.unsealWith(passphrase)
	h.typeText("new.example")
	h.press("enter", "enter")

	if ctx == nil {
		t.Fatal("CreateZone was not called")
	}

	if _, ok := ctx.Deadline(); !ok {
		t.Error("CreateZone ran without a deadline")
	}
}

func TestEscCancelsACreateInFlightWhichIsReadBack(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	h := newLostAnswerHarness(t, s, f, lostAnswer{})
	before := zonesCached(t, s)

	h.press("i")
	h.unsealWith(passphrase)
	h.typeText("new.example")
	h.press("enter")

	create := h.pressHolding("enter")
	h.assertShows("onboarding…", "cancels")

	h.press("esc")
	h.run(create)

	h.assertShows(onboardDialog, "Canceled", "no Zone of that name", "nothing was Onboarded")
	h.assertHides("is Onboarded", "onboarding…")

	if got := zonesCached(t, s); got != before {
		t.Fatalf("cached Zones = %d, want %d as before", got, before)
	}
}

func TestATenantThatCannotBeSavedOnboardsNothingAndIsStillToChoose(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)
	f.SetZoneCreator(true)
	// The credentials cannot be verified within the Tenant chosen.
	f.Fail(providertest.OpAccount, &dnsprovider.APIError{
		Provider: "cloudflare", StatusCode: 403, Messages: []string{"forbidden"},
		Kind: dnsprovider.ErrUnauthorized,
	})

	h, _ := newUpgradedHarness(t, f)

	h.press("space", "tab", "i")
	h.unsealWith(passphrase)
	h.typeText("new.example")
	h.press("enter", "enter")

	h.assertShows(onboardDialog, "Could not save Acme Ltd as the Tenant of cloudflare.old", "was Onboarded.", "Tenant     Acme Ltd")
	h.assertHides("at cloudflare.old?", "onboarding…")

	if calls := createZoneCalls(f); len(calls) != 0 {
		t.Fatalf("CreateZone calls = %q, want none", calls)
	}
}
