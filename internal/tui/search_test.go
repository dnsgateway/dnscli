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
