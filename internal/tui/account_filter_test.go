package tui_test

import (
	"testing"

	"github.com/sergelogvinov/dnscli/internal/tui"
)

// Seeded Accounts are listed as cloudflare.personal, cloudflare.work.
// cloudflare.personal hosts example.com and two.tv; cloudflare.work hosts example.org.

func TestSelectingAnAccountShowsOnlyItsZones(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("shift+tab", "enter")

	h.assertShows("example.com", "two.tv")
	h.assertHides("example.org")

	h.press("j", "enter")

	h.assertShows("example.org")
	h.assertHides("example.com", "two.tv")
}

func TestEnterAfterFilteringOpensTheZoneShownUnderTheCursor(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("j", "j") // two.tv, the last Zone
	h.press("shift+tab", "j", "enter", "tab", "enter")

	h.assertShows("No cached Records for example.org")
}

func TestClearingTheAccountFilterShowsTheZonesOfEveryAccount(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("shift+tab", "enter", "*")

	h.assertShows("example.com", "two.tv", "example.org")
}

func TestClearingAnAbsentAccountFilterKeepsTheCursor(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("j", "*", "enter")

	h.assertShows("No cached Records for example.org")
}

func TestReselectingTheSelectedAccountKeepsTheCursor(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("shift+tab", "enter", "tab", "j") // two.tv, cloudflare.personal's second Zone
	h.press("shift+tab", "enter", "tab", "enter")

	h.assertShows("198.51.100.7")
}

func TestSelectedAccountIsMarkedUntilTheFilterIsCleared(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.assertHides("✓")

	h.press("shift+tab", "j", "enter")
	h.assertShows("cloudflare.work ✓")
	h.assertHides("cloudflare.personal ✓")

	h.press("*")
	h.assertHides("✓")
}

func TestClearingTheAccountFilterUsesTheShortcutProfile(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	profile := profileWith(tui.ActionAllAccounts, "c")

	h := newHarnessWith(t, s, tui.Deps{Shortcuts: &profile})
	h.press("shift+tab", "enter", "*")
	h.assertHides("example.org")

	h.press("c")
	h.assertShows("example.org")

	h.press("?")
	h.assertShows("Zones of all Accounts")
}

func TestAccountFilterAndDomainsSearchNarrowTheZonesTogether(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("/", "e", "x")               // example.com, example.org
	h.press("shift+tab", "enter", "tab") // and cloudflare.personal's

	h.assertShows("example.com", "/ex")
	h.assertHides("example.org", "two.tv")

	// Clearing the Account filter leaves the search's result.
	h.press("*")
	h.assertShows("example.com", "example.org")
	h.assertHides("two.tv")

	// Clearing the search leaves the Account filter's result.
	h.press("shift+tab", "enter", "tab", "esc")
	h.assertShows("example.com", "two.tv")
	h.assertHides("example.org", "/ex")
}
