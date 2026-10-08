package tui_test

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providers"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// newFieldsHarness starts the model with the fake registered under each
// of the module's Provider types, with that Provider's Fields.
func newFieldsHarness(t *testing.T, s *store.Store, f *fakeProvider, fb *fieldBuilds) *harness {
	t.Helper()

	return newHarnessWith(t, s, tui.Deps{
		Providers:    f.realFieldsRegistry(fb),
		UnsealMethod: fastPassphrase(),
	})
}

func TestAddAccountShowsOneInputPerFieldOfTheChosenProvider(t *testing.T) {
	s, _ := openStore(t)
	h := newFieldsHarness(t, s, newFake(), &fieldBuilds{})

	// Cloudflare's Tenant is not typed: dnscli finds it from the token.
	h.press("i")
	h.assertShows("‹ cloudflare ›", "API token")
	h.assertHides("Account ID", "API key")

	h.press("right")
	h.assertShows("‹ godaddy ›", "API key", "API secret")
	h.assertHides("API token")

	h.press("right")
	h.assertShows("‹ namecheap ›", "API user", "API key", "Whitelisted client IPv4")
	h.assertHides("API secret")

	// Secret Fields are masked; the others show as typed.
	h.press("down", "down")
	h.typeText("apiuser")
	h.press("down")
	h.typeText("k3y")
	h.press("down")
	h.typeText("192.0.2.10")

	h.assertShows("apiuser", "•••", "192.0.2.10")
	h.assertHides("k3y")
}

// dataKey unseals the Store's data key with the test passphrase.
func dataKey(t *testing.T, s *store.Store) *vault.Key {
	t.Helper()

	slots, err := s.KeySlots(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	key, err := vault.Unseal(fastPassphrase(), slots, []byte(passphrase))
	if err != nil {
		t.Fatal(err)
	}

	return key
}

// accountLabeled is the stored Account with label.
func accountLabeled(t *testing.T, s *store.Store, label string) dns.Account {
	t.Helper()

	accounts, err := s.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	for _, a := range accounts {
		if a.Label() == label {
			return a
		}
	}

	t.Fatalf("no Account %s in %+v", label, accounts)

	return dns.Account{}
}

// storedPlaintext is what an Account's credentials are sealed as.
func storedPlaintext(t *testing.T, s *store.Store, a dns.Account) string {
	t.Helper()

	sealed, err := s.AccountSecret(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}

	plain, err := dataKey(t, s).Open(sealed, "dnscli account secret "+a.Label())
	if err != nil {
		t.Fatal(err)
	}

	return string(plain)
}

// storedFields is an Account's credentials, which must be sealed as a
// JSON field map.
func storedFields(t *testing.T, s *store.Store, a dns.Account) map[string]string {
	t.Helper()

	plain := storedPlaintext(t, s, a)

	var fields map[string]string
	if err := json.Unmarshal([]byte(plain), &fields); err != nil {
		t.Fatalf("%s's credentials are sealed as %q, not a JSON field map: %v", a.Label(), plain, err)
	}

	return fields
}

// addOldAccount stores an Account of typ as an earlier dnscli did: its
// credentials sealed as one API token.
func addOldAccount(t *testing.T, s *store.Store, typ, name, token string) dns.Account {
	t.Helper()

	a := dns.Account{Provider: typ, Name: name}

	sealed, err := dataKey(t, s).Seal([]byte(token), "dnscli account secret "+a.Label())
	if err != nil {
		t.Fatal(err)
	}

	a, err = s.AddAccountWithSecret(context.Background(), a, sealed)
	if err != nil {
		t.Fatal(err)
	}

	return a
}

// credentialCases are an Account's credentials at each of the module's
// Providers: as typed into the form, and as an earlier dnscli stored them.
var credentialCases = []struct {
	typ    string
	right  int // presses of Right from the first Provider type
	values []string
	fields map[string]string
	old    string
}{
	{"cloudflare", 0, []string{"cf-token"}, map[string]string{"token": "cf-token"}, "cf-token"},
	{"godaddy", 1, []string{"gd-key", "gd-secret"}, map[string]string{"key": "gd-key", "secret": "gd-secret"}, "gd-key:gd-secret"},
	{
		"namecheap", 2, []string{"apiuser", "apikey", "192.0.2.10"},
		map[string]string{"user": "apiuser", "key": "apikey", "client_ip": "192.0.2.10"}, "apiuser:apikey:192.0.2.10",
	},
}

// fillAccountFields adds <typ>.<name> from the Accounts pane, typing a
// value into each credential field in turn.
func (h *harness) fillAccountFields(right int, name string, values ...string) {
	h.t.Helper()

	h.press("shift+tab", "i")

	for range right {
		h.press("right")
	}

	h.press("tab")
	h.typeText(name)

	for _, v := range values {
		h.press("tab")
		h.typeText(v)
	}

	h.press("enter", "enter") // to the Save button, then save
}

func TestAddingAnAccountVerifiesThenSealsItsFieldsAsJSON(t *testing.T) {
	for _, tc := range credentialCases {
		t.Run(tc.typ, func(t *testing.T) {
			s, _ := enrollFirstAccount(t)
			f := newFake()
			fb := &fieldBuilds{}
			h := newFieldsHarness(t, s, f, fb)

			h.fillAccountFields(tc.right, "home", tc.values...)
			h.unsealWith(passphrase)

			label := tc.typ + ".home"
			h.assertShows("Discovered no Zones for " + label)

			if calls := f.Calls(); len(calls) < 2 || calls[0].Op != providertest.OpNew || calls[1].Op != providertest.OpAccount {
				t.Errorf("provider calls = %+v, want the credentials verified first", calls)
			}

			if got := storedFields(t, s, accountLabeled(t, s, label)); !maps.Equal(got, tc.fields) {
				t.Errorf("sealed fields = %v, want %v", got, tc.fields)
			}

			if got := fb.last(); !maps.Equal(got, tc.fields) {
				t.Errorf("Provider built with %v, want %v", got, tc.fields)
			}
		})
	}
}

func TestCredentialsTheProviderRefusesAreShownAndNothingIsSaved(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := newFake()
	f.Fail(providertest.OpAccount, dnsprovider.ErrUnauthorized)
	h := newFieldsHarness(t, s, f, &fieldBuilds{})

	h.fillAccountFields(1, "home", "gd-key", "wrong-secret")
	h.unsealWith(passphrase)

	h.assertShows("Add Account", "The Provider did not accept the credentials", "authorized")
	h.assertHides("Could not add")

	if accounts, _ := s.Accounts(context.Background()); len(accounts) != 1 {
		t.Fatalf("stored Accounts = %+v, want only cloudflare.work", accounts)
	}

	if n := f.count(providertest.OpZones); n != 0 {
		t.Errorf("Zones listed %d times for refused credentials", n)
	}

	// Corrected, the same dialog saves.
	f.Fail(providertest.OpAccount, nil)
	h.press("up", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace")
	h.typeText("gd-secret")
	h.press("enter", "enter")

	h.assertShows("godaddy.home")
	h.assertHides("Add Account")
}

func TestAnOldFormatAccountIsReadAndRewrittenAsJSONWhenEdited(t *testing.T) {
	for _, tc := range credentialCases {
		t.Run(tc.typ, func(t *testing.T) {
			s, _ := enrollFirstAccount(t)
			addOldAccount(t, s, tc.typ, "old", tc.old)

			f := newFake()
			fb := &fieldBuilds{}
			h := newFieldsHarness(t, s, f, fb)

			// Read lazily: the old token builds the Provider from its fields,
			// and stays as it was.
			// Accounts are listed by label: cloudflare.old comes before
			// cloudflare.work, the others after it.
			h.press("shift+tab")
			if tc.typ != "cloudflare" {
				h.press("j")
			}

			label := tc.typ + ".old"
			h.press("L")
			h.unsealWith(passphrase)
			h.assertShows("Re-listed " + label)

			if got := fb.last(); !maps.Equal(got, tc.fields) {
				t.Fatalf("Provider built with %v, want %v", got, tc.fields)
			}

			if got := storedPlaintext(t, s, accountLabeled(t, s, label)); got != tc.old {
				t.Fatalf("reading rewrote the credentials as %q", got)
			}

			// Renaming saves the Account, which seals its fields as JSON.
			h.press("e")
			h.typeText("2")

			for range len(tc.values) + 1 {
				h.press("down")
			}

			h.press("enter")
			h.assertShows("Saved " + label + "2.")

			if got := storedFields(t, s, accountLabeled(t, s, label+"2")); !maps.Equal(got, tc.fields) {
				t.Errorf("sealed fields = %v, want %v", got, tc.fields)
			}
		})
	}
}

func TestEditingOneFieldKeepsTheOthers(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	addOldAccount(t, s, "namecheap", "home", "apiuser:apikey:192.0.2.10")

	f := newFake()
	fb := &fieldBuilds{}
	h := newFieldsHarness(t, s, f, fb)

	h.press("shift+tab", "j", "e")
	h.unsealWith(passphrase)
	h.assertShows("Edit Account namecheap.home", "API user", "API key", "Whitelisted client IPv4")

	h.press("down", "down", "down")
	h.clearText(len("192.0.2.10"))
	h.typeText("198.51.100.4")
	h.press("enter", "enter")

	h.assertShows("Saved namecheap.home.")

	want := map[string]string{"user": "apiuser", "key": "apikey", "client_ip": "198.51.100.4"}
	if got := storedFields(t, s, accountLabeled(t, s, "namecheap.home")); !maps.Equal(got, want) {
		t.Errorf("sealed fields = %v, want %v", got, want)
	}

	// The new credentials were verified before they were saved.
	if got := fb.last(); !maps.Equal(got, want) {
		t.Errorf("verified with %v, want %v", got, want)
	}

	if n := f.count(providertest.OpAccount); n != 1 {
		t.Errorf("Account asked %d times, want once, to verify", n)
	}
}

func TestCredentialFieldsAreTrimmedAndBlankOnesAreMissing(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newFieldsHarness(t, s, newFake(), &fieldBuilds{})

	h.fillAccountFields(2, "home", " apiuser ", "   ", "192.0.2.10")
	h.assertShows("Enter the API key.")

	// The blank field is focused.
	h.typeText("apikey ")
	h.press("down", "down", "enter")
	h.unsealWith(passphrase)
	h.assertShows("Discovered no Zones for namecheap.home")

	want := map[string]string{"user": "apiuser", "key": "apikey", "client_ip": "192.0.2.10"}
	if got := storedFields(t, s, accountLabeled(t, s, "namecheap.home")); !maps.Equal(got, want) {
		t.Errorf("sealed fields = %v, want %v", got, want)
	}
}

func TestEditingShowsTheStoredFieldsThatAreNotSecret(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	addOldAccount(t, s, "namecheap", "home", "apiuser:apikey:192.0.2.10")

	f := newFake()
	h := newFieldsHarness(t, s, f, &fieldBuilds{})

	h.press("shift+tab", "j", "e")
	h.unsealWith(passphrase)
	h.assertShows("Edit Account namecheap.home", "apiuser", "192.0.2.10")
	h.assertHides("apikey")

	// The API key is secret: left empty, it keeps the stored one.
	h.press("down", "down")
	h.assertShows("Type a new API key")

	// Retyping a stored value changes nothing: there is nothing to verify.
	h.press("down")
	h.clearText(len("192.0.2.10"))
	h.typeText("192.0.2.10")
	h.press("enter", "enter")

	h.assertHides("Edit Account")

	if n := f.count(providertest.OpAccount); n != 0 {
		t.Errorf("Account asked %d times, want none for unchanged credentials", n)
	}
}

func TestAnAccountWithUnreadableCredentialsCanBeRenamedOrRepaired(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	addOldAccount(t, s, "godaddy", "old", "only-a-key")

	f := newFake()
	fb := &fieldBuilds{}
	h := newFieldsHarness(t, s, f, fb)

	h.press("shift+tab", "j", "e")
	h.unsealWith(passphrase)
	h.assertShows("Edit Account godaddy.old", "cannot read the stored credentials", "KEY:SECRET")

	// A rename reseals the credentials as they are.
	h.typeText("2")
	h.press("down", "down", "down", "enter")
	h.assertShows("Saved godaddy.old2.")

	if got := storedPlaintext(t, s, accountLabeled(t, s, "godaddy.old2")); got != "only-a-key" {
		t.Errorf("renaming resealed the credentials as %q, want them as they were", got)
	}

	// Typing one field cannot fill the others from them.
	h.press("e")
	h.press("down")
	h.typeText("gd-key")
	h.press("down", "down", "enter")
	h.assertShows("Enter the API secret.")

	// Typed anew in full, they replace them.
	h.typeText("gd-secret")
	h.press("down", "enter")
	h.assertShows("Saved godaddy.old2.")

	want := map[string]string{"key": "gd-key", "secret": "gd-secret"}
	if got := storedFields(t, s, accountLabeled(t, s, "godaddy.old2")); !maps.Equal(got, want) {
		t.Errorf("sealed fields = %v, want %v", got, want)
	}

	if got := fb.last(); !maps.Equal(got, want) {
		t.Errorf("verified with %v, want %v", got, want)
	}
}

func TestAnAccountOfAProviderTypeNotInThisBuildCanBeRenamed(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	addOldAccount(t, s, "route53", "old", "r53-token")

	h := newFieldsHarness(t, s, newFake(), &fieldBuilds{})

	h.press("shift+tab", "j", "j", "j", "e")
	h.unsealWith(passphrase)
	h.assertShows("Edit Account route53.old", "not available in this build")

	h.typeText("2")
	h.press("down", "enter")
	h.assertShows("Saved route53.old2.")

	if got := storedPlaintext(t, s, accountLabeled(t, s, "route53.old2")); got != "r53-token" {
		t.Errorf("renaming resealed the credentials as %q, want them as they were", got)
	}
}

func TestAnAccountWithoutCredentialsNeedsEveryField(t *testing.T) {
	s, path := enrollFirstAccount(t)
	addOldAccount(t, s, "godaddy", "home", "gd-key:gd-secret")
	execSQL(t, path, `UPDATE accounts SET secret = NULL WHERE name = 'home'`)

	f := newFake()
	h := newFieldsHarness(t, s, f, &fieldBuilds{})

	h.press("shift+tab", "j", "e")
	h.unsealWith(passphrase)

	h.press("down")
	h.typeText("gd-key")
	h.press("down", "down", "enter")
	h.assertShows("Enter the API secret.")
	h.assertHides("did not accept")

	if n := f.count(providertest.OpAccount); n != 0 {
		t.Errorf("Account asked %d times for partial credentials", n)
	}
}

func TestFieldsTheProviderCannotBeBuiltFromAreNotItsRefusal(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newHarnessWith(t, s, tui.Deps{Providers: providers.Registry(), UnsealMethod: fastPassphrase()})

	h.fillAccountFields(2, "home", "apiuser", "apikey", "not-an-ip")
	h.unsealWith(passphrase)

	h.assertShows("Add Account", "The credentials are not valid", "IPv4")
	h.assertHides("did not accept", "Could not add")
}

func TestEscCancelsVerifyingAndSavesNothing(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := newFake()
	h := newFieldsHarness(t, s, f, &fieldBuilds{})

	h.fillAccountFields(0, "home", "cf-token")
	h.typeText(passphrase)

	// Hold the save while the credentials are verified.
	unseal := h.pressHolding("enter")
	m, save := h.model.Update(unseal())
	h.model = m

	h.assertShows("adding…")
	h.press("esc")
	h.assertShows("Add Account", "Cancelled: nothing was saved.")
	h.assertHides("adding…")

	h.send(save())
	h.assertShows("Add Account", "Cancelled")

	if accounts, _ := s.Accounts(context.Background()); len(accounts) != 1 {
		t.Fatalf("stored Accounts = %+v, want only cloudflare.work", accounts)
	}

	// The dialog saves again.
	h.press("enter")
	h.assertShows("Discovered no Zones for cloudflare.home")
}
