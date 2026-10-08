package tui_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// acmeTenant is a Cloudflare account ID.
const acmeTenant = "0123456789abcdef0123456789abcdef"

// discoverRefreshAndApply adds cloudflare.work through tb's Registry,
// Refreshes work.example and applies a Changeset to it.
func discoverRefreshAndApply(t *testing.T, f *fakeProvider, tb *tenantBuilds) *harness {
	t.Helper()

	h, _, _ := newRefreshHarnessWith(t, f, tui.Deps{Providers: f.tenantRegistry(tb)})
	h.press("enter", "tab")
	h.assertShows("validated this session")
	h.buildChangeset()
	h.press("p", "enter")
	h.assertShows("Applied 3 changes to work.example.")

	return h
}

func TestAnAccountWhoseTokenReachesOneTenantActsWithinIt(t *testing.T) {
	f := refreshZones()
	f.SetTenants(dnsprovider.Tenant{ID: acmeTenant, Name: "Acme Ltd"})

	tb := &tenantBuilds{}
	discoverRefreshAndApply(t, f, tb)

	// The first build lists the Tenants, while the token is verified;
	// every later one is scoped.
	built := tb.built()
	if len(built) < 4 || built[0] != "" || slices.ContainsFunc(built[1:], func(id string) bool { return id != acmeTenant }) {
		t.Errorf("built with Tenants %q, want the first unscoped and every later one scoped to %s", built, acmeTenant)
	}

	if n := f.count(providertest.OpTenants); n != 1 {
		t.Errorf("Tenants listed %d times, want once", n)
	}
}

func TestAnAccountWhoseTokenReachesNoTenantsOrMayNotListThemRunsUnscoped(t *testing.T) {
	for name, script := range map[string]func(f *fakeProvider){
		"none": func(f *fakeProvider) {
			f.SetTenants()
		},
		"may not list": func(f *fakeProvider) {
			f.SetTenants(dnsprovider.Tenant{ID: acmeTenant})
			f.Fail(providertest.OpTenants, dnsprovider.ErrUnauthorized)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := refreshZones()
			script(f)

			tb := &tenantBuilds{}
			discoverRefreshAndApply(t, f, tb)

			if built := tb.built(); slices.ContainsFunc(built, func(id string) bool { return id != "" }) {
				t.Errorf("built with Tenants %q, want every build unscoped", built)
			}

			if n := f.count(providertest.OpTenants); n != 1 {
				t.Errorf("Tenants listed %d times, want once", n)
			}
		})
	}
}

func TestAnAccountOwnedTokenIsVerifiedWithinItsTenant(t *testing.T) {
	f := refreshZones()
	f.SetTenants(dnsprovider.Tenant{ID: acmeTenant, Name: "Acme Ltd"})

	s, _ := openStore(t)
	h := newHarnessWith(t, s, tui.Deps{Providers: f.tenantRegistry(&tenantBuilds{}), UnsealMethod: fastPassphrase()})

	// Cloudflare verifies an account-owned token only within its account,
	// which the form does not ask for.
	h.fillAddAccount("work", ownedTokenPrefix+"tok")
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")

	h.assertShows("work.example")
	h.assertHides("did not accept")
}

// newTenantHarness starts the model with the fake registered as
// "cloudflare", taking a Tenant in its "account" field, and no Accounts.
func newTenantHarness(t *testing.T, f *fakeProvider) (*harness, *store.Store) {
	t.Helper()

	s, _ := openStore(t)

	return newHarnessWith(t, s, tui.Deps{Providers: f.tenantRegistry(&tenantBuilds{}), UnsealMethod: fastPassphrase()}), s
}

// enrollPassphrase answers the prompt to choose the first passphrase.
func (h *harness) enrollPassphrase() {
	h.t.Helper()

	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")
}

func TestAddingAnAccountWhoseTokenReachesOneTenantStoresItAndAsksNothingMore(t *testing.T) {
	f := refreshZones()
	f.SetTenants(dnsprovider.Tenant{ID: acmeTenant, Name: "Acme Ltd"})

	h, s := newTenantHarness(t, f)
	h.fillAddAccount("work", "tok")
	h.enrollPassphrase()

	h.assertShows("work.example")

	if got, want := storedFields(t, s, accountLabeled(t, s, "cloudflare.work")), map[string]string{"token": "tok", "account": acmeTenant}; !maps.Equal(got, want) {
		t.Errorf("stored credentials = %v, want %v", got, want)
	}
}

// twoTenants are the Tenants a token reaching several lists.
var twoTenants = []dnsprovider.Tenant{{ID: acmeTenant, Name: "Acme Ltd"}, {ID: "b2", Name: "Side project"}}

func TestAddingAnAccountWhoseTokenReachesSeveralTenantsAsksWhichAndNamesItAfterIt(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)

	h, s := newTenantHarness(t, f)

	// The name is left empty: the Tenant's is suggested.
	h.fillAddAccount("", "tok")
	h.enrollPassphrase()

	h.assertShows("Tenant", "Acme Ltd", "Side project", "pick the one")
	h.assertHides("work.example")

	for _, c := range f.Calls() {
		if c.Op != providertest.OpNew && c.Op != providertest.OpTenants {
			t.Errorf("called %s before a Tenant was picked", c.Op)
		}
	}

	// The picked Tenant's name is suggested in the Name field, and
	// nothing is saved until the user saves it.
	h.press("down", "enter")
	h.assertShows("Add Account", "side-project", "cloudflare.side-project")
	noAccounts(t, s)

	h.press("enter")
	h.assertShows("work.example", "cloudflare.side-project")

	if got, want := storedFields(t, s, accountLabeled(t, s, "cloudflare.side-project")), map[string]string{"token": "tok", "account": "b2"}; !maps.Equal(got, want) {
		t.Errorf("stored credentials = %v, want %v", got, want)
	}
}

// noAccounts fails t when s holds an Account.
func noAccounts(t *testing.T, s *store.Store) {
	t.Helper()

	if accounts, err := s.Accounts(context.Background()); err != nil || len(accounts) != 0 {
		t.Errorf("Accounts = %+v, %v; want none saved", accounts, err)
	}
}

func TestTheOnlyTenantsNameIsSuggestedForTheUserToAcceptOrEdit(t *testing.T) {
	for name, tc := range map[string]struct {
		tenant  string
		suggest string
		edit    []string // keys that edit the suggestion from the Save button
		label   string
	}{
		"accepted":        {tenant: "Acme Ltd", suggest: "acme-ltd", label: "cloudflare.acme-ltd"},
		"a user's":        {tenant: "user@example.com's Account", suggest: "user-example-com", label: "cloudflare.user-example-com"},
		"edited":          {tenant: "Acme Ltd", suggest: "acme-ltd", edit: []string{"up", "up", "backspace", "backspace", "backspace", "backspace", "down", "down"}, label: "cloudflare.acme"},
		"an apostrophe's": {tenant: "Bob's DNS", suggest: "bobs-dns", label: "cloudflare.bobs-dns"},
	} {
		t.Run(name, func(t *testing.T) {
			f := refreshZones()
			f.SetTenants(dnsprovider.Tenant{ID: acmeTenant, Name: tc.tenant})

			h, s := newTenantHarness(t, f)
			h.fillAddAccount("", "tok")
			h.enrollPassphrase()

			h.assertShows("Add Account", "cloudflare."+tc.suggest)
			noAccounts(t, s)

			h.press(tc.edit...)
			h.press("enter")
			h.assertShows("work.example", tc.label)

			if got := storedFields(t, s, accountLabeled(t, s, tc.label))["account"]; got != acmeTenant {
				t.Errorf("stored Tenant = %q, want %s", got, acmeTenant)
			}
		})
	}
}

func TestAPickedTenantKeepsTheNameTyped(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)

	h, s := newTenantHarness(t, f)
	h.fillAddAccount("work", "tok")
	h.enrollPassphrase()
	h.press("enter")
	h.assertShows("work.example")

	if got := storedFields(t, s, accountLabeled(t, s, "cloudflare.work"))["account"]; got != acmeTenant {
		t.Errorf("stored Tenant = %q, want %s", got, acmeTenant)
	}
}

func TestClosingTheTenantPickerSavesNothing(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)

	h, s := newTenantHarness(t, f)
	h.fillAddAccount("work", "tok")
	h.enrollPassphrase()
	h.press("esc")

	h.assertShows("Add Account", "Pick a Tenant")

	if accounts, err := s.Accounts(context.Background()); err != nil || len(accounts) != 0 {
		t.Errorf("Accounts = %+v, %v; want none saved", accounts, err)
	}
}

func TestAnAccountNameIsNeededWhenNoTenantNamesIt(t *testing.T) {
	f := refreshZones()

	h, s := newTenantHarness(t, f)
	h.fillAddAccount("", "tok")
	h.enrollPassphrase()

	h.assertShows("Enter a name for the Account.")

	if accounts, err := s.Accounts(context.Background()); err != nil || len(accounts) != 0 {
		t.Errorf("Accounts = %+v, %v; want none saved", accounts, err)
	}
}

func TestTheNameHintSaysAnEmptyNameIsTheTenants(t *testing.T) {
	h, _ := newTenantHarness(t, refreshZones())
	h.press("i", "tab")

	h.assertShows("empty, the Tenant's name is suggested")
}

// newUpgradedHarness starts a session on a Store holding cloudflare.work
// and cloudflare.old, an Account an earlier dnscli stored with its API
// token alone, no Tenant, with the fake registered as in tenantRegistry.
// The cursor is on cloudflare.old in the Accounts pane.
func newUpgradedHarness(t *testing.T, f *fakeProvider) (*harness, *store.Store) {
	t.Helper()

	return newHarnessHolding(t, f, "tok")
}

// newHarnessHolding is newUpgradedHarness with cloudflare.old's
// credentials sealed as plain.
func newHarnessHolding(t *testing.T, f *fakeProvider, plain string) (*harness, *store.Store) {
	t.Helper()

	s, _ := enrollFirstAccount(t)
	addOldAccount(t, s, "cloudflare", "old", plain)

	h := newHarnessWith(t, s, tui.Deps{Providers: f.tenantRegistry(&tenantBuilds{}), UnsealMethod: fastPassphrase()})
	h.press("shift+tab")

	return h, s
}

func TestAnUpgradedAccountIsPinnedToTheOnlyTenantItReachesOnFirstUse(t *testing.T) {
	f := refreshZones()
	f.SetTenants(dnsprovider.Tenant{ID: acmeTenant, Name: "Acme Ltd"})

	h, s := newUpgradedHarness(t, f)
	h.press("L")
	h.unsealWith(passphrase)

	h.assertShows("Re-listed cloudflare.old")
	h.assertHides("Tenant")

	if got, want := storedFields(t, s, accountLabeled(t, s, "cloudflare.old")), map[string]string{"token": "tok", "account": acmeTenant}; !maps.Equal(got, want) {
		t.Errorf("stored credentials = %v, want %v", got, want)
	}
}

// onlyTenantsListed fails t when the fake was asked for anything but its
// Tenants, besides being built.
func onlyTenantsListed(t *testing.T, f *fakeProvider) {
	t.Helper()

	for _, c := range f.Calls() {
		if c.Op != providertest.OpNew && c.Op != providertest.OpTenants {
			t.Errorf("called %s before a Tenant was picked", c.Op)
		}
	}
}

func TestAnUpgradedAccountReachingSeveralTenantsAsksForOneOnFirstUse(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)

	h, s := newUpgradedHarness(t, f)
	h.press("L")
	h.unsealWith(passphrase)

	h.assertShows("Tenant of cloudflare.old", "Acme Ltd", "Side project")
	onlyTenantsListed(t, f)

	// Picked, the Tenant is saved, and the re-listing runs again.
	h.press("down", "enter")
	h.assertShows("Re-listed cloudflare.old")
	h.assertHides("Tenant of")

	if got, want := storedFields(t, s, accountLabeled(t, s, "cloudflare.old")), map[string]string{"token": "tok", "account": "b2"}; !maps.Equal(got, want) {
		t.Errorf("stored credentials = %v, want %v", got, want)
	}
}

func TestARefreshOfAnUpgradedAccountReachingSeveralTenantsAsksForOne(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)

	h, s := newUpgradedHarness(t, f)
	old := accountLabeled(t, s, "cloudflare.old")
	mustZone(t, s, dns.Zone{AccountID: old.ID, Name: "old.example", ProviderID: "zw2"})
	h.run(h.model.Init())

	// Show cloudflare.old's Zones, and open old.example, which Refreshes it.
	h.press("enter", "tab", "enter")
	h.unsealWith(passphrase)

	h.assertShows("Tenant of cloudflare.old")
	onlyTenantsListed(t, f)

	// Picked, the Tenant is saved, and the Refresh runs again.
	h.press("enter")
	h.assertShows("cloudflare.old acts within Acme Ltd.")

	if n := f.count(providertest.OpRecords); n != 1 {
		t.Errorf("Records read %d times, want once, after the Tenant was picked", n)
	}

	if got := storedFields(t, s, old)["account"]; got != acmeTenant {
		t.Errorf("stored Tenant = %q, want %s", got, acmeTenant)
	}
}

func TestAClosedTenantPickerIsOpenedAgainOnTheNextUse(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)

	h, s := newUpgradedHarness(t, f)
	h.press("L")
	h.unsealWith(passphrase)
	h.press("esc")

	h.assertHides("Tenant of")
	h.assertShows("cloudflare.old acts within no Tenant yet")

	if plain := storedPlaintext(t, s, accountLabeled(t, s, "cloudflare.old")); plain != "tok" {
		t.Errorf("credentials sealed as %q, want them untouched", plain)
	}

	h.press("L")
	h.assertShows("Tenant of cloudflare.old")
	onlyTenantsListed(t, f)

	if n := f.count(providertest.OpTenants); n != 1 {
		t.Errorf("Tenants listed %d times, want once this session", n)
	}
}

func TestEditingInATokenReachingSeveralTenantsAsksWhichAndKeepsTheName(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)

	h, s := newUpgradedHarness(t, f)
	h.press("e")
	h.unsealWith(passphrase)
	h.assertShows("Edit Account cloudflare.old")

	// A new API token, then Save.
	h.press("down")
	h.typeText("tok2")
	h.press("down", "enter")

	h.assertShows("Tenant", "Acme Ltd", "Side project")
	h.press("enter")
	h.assertShows("Saved cloudflare.old.")

	if got, want := storedFields(t, s, accountLabeled(t, s, "cloudflare.old")), map[string]string{"token": "tok2", "account": acmeTenant}; !maps.Equal(got, want) {
		t.Errorf("stored credentials = %v, want %v", got, want)
	}
}

func TestEditingTheTokenOfAnAccountWithATenantFindsItsTenantAgain(t *testing.T) {
	for name, tc := range map[string]struct {
		tenants []dnsprovider.Tenant
		pick    []string // the keys that pick a Tenant; none when not asked
	}{
		"one":     {tenants: twoTenants[1:]},
		"several": {tenants: twoTenants, pick: []string{"down", "enter"}},
	} {
		t.Run(name, func(t *testing.T) {
			f := refreshZones()
			f.SetTenants(tc.tenants...)

			// cloudflare.old acts within Acme Ltd; its new token does not.
			h, s := newHarnessHolding(t, f, `{"token":"tok","account":"`+acmeTenant+`"}`)
			h.press("e")
			h.unsealWith(passphrase)
			h.assertShows("Edit Account cloudflare.old")
			h.press("down")
			h.typeText("tok2")
			h.press("down", "enter")

			if tc.pick != nil {
				h.assertShows("Acme Ltd", "Side project")
				h.press(tc.pick...)
			}

			h.assertShows("Saved cloudflare.old.")

			if got, want := storedFields(t, s, accountLabeled(t, s, "cloudflare.old")), map[string]string{"token": "tok2", "account": "b2"}; !maps.Equal(got, want) {
				t.Errorf("stored credentials = %v, want %v", got, want)
			}
		})
	}
}

func TestATenantListThatCannotReachTheProviderIsNotARefusal(t *testing.T) {
	for name, err := range map[string]error{
		"unavailable":  &dnsprovider.APIError{Provider: "cloudflare", StatusCode: 503},
		"rate limited": &dnsprovider.APIError{Provider: "cloudflare", StatusCode: 429},
		"network":      errors.New("dial tcp: connection refused"),
	} {
		t.Run(name, func(t *testing.T) {
			f := refreshZones()
			f.SetTenants(twoTenants...)
			f.Fail(providertest.OpTenants, err)

			h, s := newTenantHarness(t, f)
			h.fillAddAccount("work", "tok")
			h.enrollPassphrase()

			h.assertShows("Could not reach the Provider:")
			h.assertHides("did not accept")

			if accounts, err := s.Accounts(context.Background()); err != nil || len(accounts) != 0 {
				t.Errorf("Accounts = %+v, %v; want none saved", accounts, err)
			}
		})
	}
}

func TestCredentialsTheProviderRefusesAreStillRefused(t *testing.T) {
	f := refreshZones()
	f.Fail(providertest.OpAccount, &dnsprovider.APIError{Provider: "cloudflare", StatusCode: 403})

	h, _ := newTenantHarness(t, f)
	h.fillAddAccount("work", "tok")
	h.enrollPassphrase()

	h.assertShows("The Provider did not accept the credentials:")
}

// replaceCredentials seals plain as a's credentials, as another dnscli
// would save them.
func replaceCredentials(t *testing.T, s *store.Store, a dns.Account, plain string) {
	t.Helper()

	sealed, err := dataKey(t, s).Seal([]byte(plain), "dnscli account secret "+a.Label())
	if err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateAccountWithSecret(context.Background(), a, sealed); err != nil {
		t.Fatal(err)
	}
}

func TestAnApplyThatNeedsATenantAsksForOneAndIsConfirmedAgain(t *testing.T) {
	f := refreshZones()
	f.SetTenants(dnsprovider.Tenant{ID: acmeTenant, Name: "Acme Ltd"})

	h, s, _ := newRefreshHarnessWith(t, f, tui.Deps{Providers: f.tenantRegistry(&tenantBuilds{})})
	h.press("enter", "tab")
	h.assertShows("validated this session")
	h.buildChangeset()

	// Meanwhile, another dnscli saved a token reaching several Tenants.
	f.SetTenants(twoTenants...)
	replaceCredentials(t, s, accountLabeled(t, s, "cloudflare.work"), `{"token":"tok2"}`)

	writes := func() int {
		return f.count(providertest.OpApply) + f.count(providertest.OpCreateRecord) +
			f.count(providertest.OpUpdateRecord) + f.count(providertest.OpDeleteRecord)
	}

	h.press("p", "enter")
	h.assertShows("Tenant of cloudflare.work", "Acme Ltd", "Side project")

	if n := writes(); n != 0 {
		t.Fatalf("%d writes before a Tenant was picked, want none", n)
	}

	// Picked, the Tenant is saved, and the changes are shown again to be
	// applied: they are not sent unconfirmed.
	h.press("down", "enter")
	h.assertShows("cloudflare.work acts within Side project", "Changes to work.example")

	if n := writes(); n != 0 {
		t.Fatalf("%d writes before the Apply was confirmed again, want none", n)
	}

	h.press("enter")
	h.assertShows("Applied 3 changes to work.example.")

	if got := storedFields(t, s, accountLabeled(t, s, "cloudflare.work"))["account"]; got != "b2" {
		t.Errorf("stored Tenant = %q, want b2", got)
	}
}

func TestEveryRefreshThatAskedForTheTenantRunsAgainOnceItIsPicked(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)

	h, s := newUpgradedHarness(t, f)
	old := accountLabeled(t, s, "cloudflare.old")
	mustZone(t, s, dns.Zone{AccountID: old.ID, Name: "old.example", ProviderID: "zw1"})
	mustZone(t, s, dns.Zone{AccountID: old.ID, Name: "old.test", ProviderID: "zw2"})
	h.run(h.model.Init())

	// Unseal, and leave the picker the re-listing opens.
	h.press("L")
	h.unsealWith(passphrase)
	h.press("esc")

	// Open old.example, holding its Refresh back, then old.test.
	h.press("enter", "tab")
	m, refreshA := h.model.Update(h.pressHolding("enter")())
	h.model = m
	h.press("j", "enter")

	h.assertShows("Tenant of cloudflare.old")
	h.run(refreshA)
	onlyTenantsListed(t, f)

	h.press("enter")
	h.assertShows("cloudflare.old acts within Acme Ltd.")

	if n := f.count(providertest.OpRecords); n != 2 {
		t.Errorf("Records read %d times after the Tenant was picked, want once for each Zone", n)
	}
}

func TestAnEditDialogDoesNotOpenOverTheTenantPicker(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)

	h, s := newUpgradedHarness(t, f)
	h.press("L")
	h.unsealWith(passphrase)
	h.press("esc")

	// The edit dialog reads the credentials while a re-listing asks for
	// the Tenant.
	readCredentials := h.pressHolding("e")
	h.press("L")
	h.assertShows("Tenant of cloudflare.old")
	h.run(readCredentials)

	h.assertShows("Tenant of cloudflare.old")
	h.assertHides("Edit Account")

	// Picked, the edit dialog opens.
	h.press("enter")
	h.assertShows("Edit Account cloudflare.old")

	if got := storedFields(t, s, accountLabeled(t, s, "cloudflare.old"))["account"]; got != acmeTenant {
		t.Errorf("stored Tenant = %q, want %s", got, acmeTenant)
	}
}

func TestTheTenantPickerActsOnTheAccountAsItIsWhenPicked(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)

	h, s := newUpgradedHarness(t, f)
	h.press("L")
	h.unsealWith(passphrase)
	h.press("esc")

	// A re-listing asks for the Tenant once the Account was renamed.
	relisted := h.pressHolding("L")()

	h.press("e")
	h.assertShows("Edit Account cloudflare.old")
	h.typeText("2")
	h.press("down", "down", "enter")
	h.assertShows("Saved cloudflare.old2.")

	h.send(relisted)
	h.assertShows("Tenant of cloudflare.old2")
	h.press("enter")
	h.assertShows("Re-listed cloudflare.old2")
	h.assertHides("Could not")

	if got := storedFields(t, s, accountLabeled(t, s, "cloudflare.old2"))["account"]; got != acmeTenant {
		t.Errorf("stored Tenant = %q, want %s", got, acmeTenant)
	}
}
