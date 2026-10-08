package tui_test

import (
	"testing"

	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
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
