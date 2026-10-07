package tui_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// fastPassphrase is the passphrase Unseal Method with the cheapest argon2id
// cost, so tests stay fast. Real sessions use vault.DefaultArgon2.
func fastPassphrase() vault.Method {
	return vault.Passphrase{Cost: vault.Argon2Cost{Time: 1, MemoryKiB: 8, Threads: 1}}
}

// newAccountHarness starts the model with a fake Provider registered as
// "cloudflare" and the fast passphrase Unseal Method.
func newAccountHarness(t *testing.T, s *store.Store, f *fake.Provider) *harness {
	t.Helper()

	return newHarnessWith(t, s, tui.Deps{
		Providers:    provider.Registry{"cloudflare": f.New},
		UnsealMethod: fastPassphrase(),
	})
}

func TestAddAccountDialogAsksForProviderNameAndToken(t *testing.T) {
	s, _ := openStore(t)
	h := newAccountHarness(t, s, fake.New())

	h.press("a")

	h.assertShows("Add Account", "Provider", "cloudflare", "Name", "API token")
	// The first Account carries the warning that the database is not disposable.
	h.assertShows("not a disposable cache")
}

const token = "cf-token-Zx81-secret"

// workZones scripts the fake Provider with two Zones.
func workZones() *fake.Provider {
	return fake.New().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2"})
}

// typeText presses each character of s.
func (h *harness) typeText(s string) {
	h.t.Helper()

	for _, r := range s {
		h.press(string(r))
	}
}

// fillAddAccount opens the add-Account dialog and submits it for
// cloudflare.<name> with the API token.
func (h *harness) fillAddAccount(name, token string) {
	h.t.Helper()

	h.press("a", "tab")
	h.typeText(name)
	h.press("tab")
	h.typeText(token)
	h.press("enter")
}

func TestAddingTheFirstAccountEnrollsAPassphraseAndDiscoversItsZones(t *testing.T) {
	s, path := openStore(t)
	f := workZones()
	h := newAccountHarness(t, s, f)

	h.fillAddAccount("work", token)

	// The first secret needs a data key: the user chooses a passphrase.
	h.assertShows("Choose a passphrase", "unrecoverable")
	h.assertHides("work.example")

	if accounts, _ := s.Accounts(context.Background()); len(accounts) != 0 {
		t.Fatalf("Account stored before Unseal: %+v", accounts)
	}

	h.typeText("correct horse")
	h.press("tab")
	h.typeText("correct horse")
	h.press("enter")

	h.assertShows("cloudflare.work", "work.example", "work.test", "unsealed")
	h.assertHides("Choose a passphrase", "Add Account")

	accounts, err := s.Accounts(context.Background())
	if err != nil || len(accounts) != 1 || accounts[0].Label() != "cloudflare.work" {
		t.Fatalf("stored accounts = %+v, %v", accounts, err)
	}

	// The fake Provider was built with the token decrypted from the Store.
	calls := f.Calls()
	if len(calls) != 2 || calls[0].Op != fake.OpNew || calls[0].Token != token || calls[0].Account.Label() != "cloudflare.work" || calls[1].Op != fake.OpZones {
		t.Fatalf("provider calls = %+v", calls)
	}

	zones, err := s.Zones(context.Background())
	if err != nil || len(zones) != 2 || zones[0].Name != "work.example" || zones[0].ProviderID != "zw1" || zones[0].AccountID != accounts[0].ID {
		t.Fatalf("cached zones = %+v, %v", zones, err)
	}

	assertNotInDatabase(t, path, token)
}

const passphrase = "correct horse"

// enrollFirstAccount adds cloudflare.work in a first session, enrolling the
// passphrase, and returns the Store for a later session to start on.
func enrollFirstAccount(t *testing.T) (*store.Store, string) {
	t.Helper()

	s, path := openStore(t)
	h := newAccountHarness(t, s, workZones())
	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")
	h.assertShows("work.example")

	return s, path
}

func TestALaterSessionStartsSealedAndBrowsingNeverPrompts(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := workZones()
	h := newAccountHarness(t, s, f)

	h.assertShows("sealed · read-only", "cloudflare.work", "work.example")

	h.press("enter", "j", "/", "w", "esc", "tab", "enter", "*", "tab", "?", "esc")

	h.assertHides("Unseal", "Passphrase")
	h.assertShows("sealed · read-only")

	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("browsing called the Provider: %+v", calls)
	}
}

func TestAWrongPassphraseIsReportedAndTheSessionStaysSealed(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := fake.New().AddZone(dns.Zone{Name: "home.example", ProviderID: "zh1"})
	h := newAccountHarness(t, s, f)

	h.fillAddAccount("home", "home-token")
	h.assertShows("Unseal", "Passphrase", "needs your Account secrets")
	h.assertHides("Choose a passphrase")

	h.typeText("wrong horse")
	h.press("enter")

	h.assertShows("Wrong passphrase", "still sealed", "sealed · read-only")

	if accounts, _ := s.Accounts(context.Background()); len(accounts) != 1 {
		t.Fatalf("an Account was stored while sealed: %+v", accounts)
	}

	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("the Provider was called while sealed: %+v", calls)
	}

	h.typeText(passphrase)
	h.press("enter")

	h.assertShows("unsealed", "cloudflare.home", "home.example")
	h.assertHides("Wrong passphrase")
}

func TestOneUnsealPerSessionUnlocksEveryAccount(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := fake.New()
	h := newAccountHarness(t, s, f)

	h.fillAddAccount("home", "home-token")
	h.typeText(passphrase)
	h.press("enter")
	h.assertShows("cloudflare.home", "unsealed")

	h.fillAddAccount("lab", "lab-token")

	h.assertHides("Unseal ", "Passphrase")
	h.assertShows("cloudflare.lab")

	var tokens []string

	for _, c := range f.Calls() {
		if c.Op == fake.OpNew {
			tokens = append(tokens, c.Account.Label()+"="+c.Token)
		}
	}

	if want := []string{"cloudflare.home=home-token", "cloudflare.lab=lab-token"}; !slices.Equal(tokens, want) {
		t.Fatalf("Provider built with %v, want %v", tokens, want)
	}
}

func TestTheAddAccountDialogTypesKeysThatAreBoundToActions(t *testing.T) {
	s, _ := openStore(t)
	h := newAccountHarness(t, s, workZones())

	// q quits, ? opens help, j moves and a adds an Account outside the dialog.
	h.press("a", "tab")
	h.typeText("qja?")

	if h.quit {
		t.Fatal("q in the Name field quit the app")
	}

	h.assertShows("qja?")
	h.assertHides("toggle help")

	h.press("backspace")
	h.assertShows("qja")
	h.assertHides("qja?")

	// Esc cancels: nothing is stored and no Unseal is asked for.
	h.press("esc")
	h.assertHides("Add Account", "Choose a passphrase")

	if accounts, _ := s.Accounts(context.Background()); len(accounts) != 0 {
		t.Fatalf("a cancelled dialog stored %+v", accounts)
	}

	h.press("a", "ctrl+c")

	if !h.quit {
		t.Fatal("Ctrl+C did not quit from the dialog")
	}
}

func TestTheAddAccountDialogRefusesIncompleteInput(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newAccountHarness(t, s, fake.New())

	h.fillAddAccount("", "t")
	h.assertShows("Enter a name for the Account.")

	h.press("esc")
	h.fillAddAccount("home", "")
	h.assertShows("Enter the API token.")

	h.press("esc")
	h.fillAddAccount("work", "t")
	h.assertShows("cloudflare.work already exists.")
	h.assertHides("Passphrase")
}

func TestMismatchedPassphrasesAreNotEnrolled(t *testing.T) {
	s, _ := openStore(t)
	h := newAccountHarness(t, s, workZones())

	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText("correct horsE")
	h.press("enter")

	h.assertShows("do not match", "sealed · read-only")

	if slots, _ := s.KeySlots(context.Background()); len(slots) != 0 {
		t.Fatalf("mismatched passphrases enrolled %+v", slots)
	}
}

func TestFailedZoneDiscoveryIsShownAndKeepsTheAccount(t *testing.T) {
	s, _ := openStore(t)
	f := workZones()
	f.Fail(fake.OpZones, errors.New("API is down"))
	h := newAccountHarness(t, s, f)

	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")

	h.assertShows("cloudflare.work", "Zone discovery for cloudflare.work failed", "API is down")
	h.assertHides("work.example")
}

func TestWithoutProviderTypesNoAccountCanBeAdded(t *testing.T) {
	s, _ := openStore(t)
	h := newHarnessWith(t, s, tui.Deps{UnsealMethod: fastPassphrase()})

	h.fillAddAccount("work", token)

	h.assertShows("No Provider types are available")
	h.assertHides("Choose a passphrase")
}

func TestThePassphraseIsNotStored(t *testing.T) {
	_, path := enrollFirstAccount(t)

	assertNotInDatabase(t, path, passphrase)
}

// assertNotInDatabase checks that text appears nowhere in the database
// files, including the WAL.
func assertNotInDatabase(t *testing.T, path, text string) {
	t.Helper()

	for _, p := range []string{path, path + "-wal"} {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			t.Fatal(err)
		}

		if bytes.Contains(b, []byte(text)) {
			t.Fatalf("%s holds %q in plaintext", filepath.Base(p), text)
		}
	}
}
