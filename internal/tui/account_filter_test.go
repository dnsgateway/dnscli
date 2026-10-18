// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

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
	h.press("shift+tab", "space")

	h.assertShows("example.com", "two.tv")
	h.assertHides("example.org")

	h.press("j", "space")

	h.assertShows("example.org")
	h.assertHides("example.com", "two.tv")
}

func TestEnterSelectsLikeSpaceInTheDomainsAndAccountsPanes(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("shift+tab", "j", "enter")

	h.assertShows("example.org")
	h.assertHides("example.com", "two.tv", "Edit Account")

	h.press("tab", "enter")
	h.assertShows("No cached Records for example.org")
	h.assertHides("Edit Zone")
}

func TestSpaceAfterFilteringOpensTheZoneShownUnderTheCursor(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("j", "j") // two.tv, the last Zone
	h.press("shift+tab", "j", "space", "tab", "space")

	h.assertShows("No cached Records for example.org")
}

func TestSelectingTheSelectedAccountAgainClearsTheFilter(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("shift+tab", "space")
	h.assertHides("example.org")

	h.press("space")
	h.assertShows("example.com", "two.tv", "example.org")
	h.assertHides("✓")
}

func TestSelectingAnotherAccountMovesTheFilterToIt(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("shift+tab", "space", "j", "space")

	h.assertShows("example.org", "cloudflare.work ✓")
	h.assertHides("example.com", "two.tv")
}

func TestSelectedAccountIsMarkedUntilTheFilterIsCleared(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.assertHides("✓")

	h.press("shift+tab", "j", "space")
	h.assertShows("cloudflare.work ✓")
	h.assertHides("cloudflare.personal ✓")

	h.press("space")
	h.assertHides("✓")
}

func TestTheAccountFilterKeyUsesTheShortcutProfile(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	profile := profileWith(tui.ActionShow, "c")

	h := newHarnessWith(t, s, tui.Deps{Shortcuts: &profile})
	h.press("shift+tab", "c")
	h.assertHides("example.org")

	h.press("c")
	h.assertShows("example.org")

	h.press("?")
	h.assertShows("c ", "show only the Account's Zones")
}

func TestAccountFilterAndDomainsSearchNarrowTheZonesTogether(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("/", "e", "x")               // example.com, example.org
	h.press("shift+tab", "space", "tab") // and cloudflare.personal's

	h.assertShows("example.com", "/ex")
	h.assertHides("example.org", "two.tv")

	// Clearing the Account filter leaves the search's result.
	h.press("shift+tab", "space", "tab")
	h.assertShows("example.com", "example.org")
	h.assertHides("two.tv")

	// Clearing the search leaves the Account filter's result.
	h.press("shift+tab", "space", "tab", "esc")
	h.assertShows("example.com", "two.tv")
	h.assertHides("example.org", "/ex")
}
