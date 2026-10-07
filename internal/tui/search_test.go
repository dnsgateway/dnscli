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

func TestBackspaceWidensTheSearch(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("/", "e", "x", "x")
	h.assertHides("example.com")

	h.press("backspace")
	h.assertShows("/ex", "example.com", "example.org")
	h.assertHides("/exx", "two.tv")

	// Still typing.
	h.press("a", "m", "p", "l", "e", ".", "o")
	h.assertShows("example.org")
	h.assertHides("example.com")
}

// example.com's seeded Records:
//
//	example.com      A      192.0.2.10
//	example.com      AAAA   2001:db8::10
//	www.example.com  CNAME  example.com

func TestSlashInRecordsFiltersByName(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("enter", "shift+tab", "/", "w", "w", "w")

	h.assertShows("/www", "www.example.com", "CNAME")
	h.assertHides("192.0.2.10", "2001:db8::10")
}

func TestSlashInRecordsFiltersByType(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("enter", "shift+tab", "/", "a", "a", "a", "a")

	h.assertShows("2001:db8::10")
	h.assertHides("192.0.2.10", "www.example.com")
}

func TestSlashInRecordsFiltersByValue(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("enter", "shift+tab", "/", "1", "9", "2")

	h.assertShows("192.0.2.10")
	h.assertHides("2001:db8::10", "www.example.com")
}

func TestEscClearsTheRecordsSearchAndRestoresAllRecords(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("enter", "shift+tab", "/", "w", "w", "w", "esc")

	h.assertShows("192.0.2.10", "2001:db8::10", "www.example.com")
	h.assertHides("/www")
}

func TestOpeningAnotherZoneStartsWithAllItsRecords(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("enter", "shift+tab", "/", "w", "w", "w", "tab", "j", "j", "enter")

	h.assertShows("198.51.100.7")
	h.assertHides("/www")
}

func TestSearchWithoutMatchesSaysSo(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("enter", "/", "z", "z")
	h.assertShows("No matching Zones")

	h.press("esc", "shift+tab", "/", "z", "z")
	h.assertShows("No matching Records")
	h.assertHides("No matching Zones")
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
