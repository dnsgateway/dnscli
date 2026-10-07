package tui_test

import (
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

var _ = dns.Zone{}
