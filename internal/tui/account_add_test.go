package tui_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
