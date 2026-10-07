package tui_test

import "testing"

// Seeded Zones are listed by zone name: example.com, example.org, two.tv.

func TestSlashInDomainsFiltersZonesByNameAsTheUserTypes(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("/", "e", "x")

	h.assertShows("example.com", "example.org")
	h.assertHides("two.tv")

	h.press("a", "m", "p", "l", "e", ".", "o")

	h.assertShows("example.org")
	h.assertHides("example.com", "two.tv")
}

func TestEscClearsTheDomainsSearchAndRestoresAllZones(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("/", "t", "w", "o", "esc")

	h.assertShows("example.com", "example.org", "two.tv")
	h.assertHides("/two")

	// Search is left too: keys are actions again.
	h.press("j", "enter")
	h.assertShows("No cached Records for example.org")
}

func TestEnterOpensTheHighlightedMatchAndKeepsTheFilter(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("/", "e", "x", "down", "enter")

	h.assertShows("No cached Records for example.org", "/ex")
	h.assertHides("two.tv")

	// Enter leaves the search input: keys are actions again.
	h.press("k", "enter")
	h.assertShows("192.0.2.10", "/ex")
}

func TestKeysTypedIntoSearchDoNotTriggerActions(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("/", "q", "?", "j", "k")

	if h.quit {
		t.Fatal("typing q into search quit the app")
	}

	h.assertShows("/q?jk")
	h.assertHides("next pane", "example.com")
}
