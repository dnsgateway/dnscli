package tui_test

import "testing"

// Seeded Zones are listed by zone name: example.com, example.org, two.tv.

func TestRecordsPaneAsksToOpenAZoneBeforeOneIsOpened(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.assertShows("Press Enter on a Zone")
	h.assertHides("192.0.2.10", "198.51.100.7")
}

func TestEnterOnZoneShowsItsCachedRecords(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("enter")

	h.assertShows("Name", "Type", "Content", "TTL")
	h.assertShows("192.0.2.10", "2001:db8::10", "www.example.com", "CNAME", "3600")
	h.assertHides("198.51.100.7", "Press Enter on a Zone")
	h.assertDimmed("192.0.2.10")
}

func TestJAndDownMoveToLaterZones(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("j", "down", "enter")

	h.assertShows("198.51.100.7")
	h.assertHides("192.0.2.10")
}

func TestKAndUpMoveToEarlierZones(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("j", "j", "k", "up", "enter")

	h.assertShows("192.0.2.10")
}

func TestMovementStopsAtTheEndsOfTheList(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.press("k", "enter")
	h.assertShows("192.0.2.10")

	h.press("j", "j", "j", "j", "enter")
	h.assertShows("198.51.100.7")
}

func TestZoneWithoutCachedRecordsSaysSo(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("j", "enter")

	h.assertShows("No cached Records")
}

func TestStatusBarShowsOpenZoneAndLastRefresh(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.press("enter")
	h.assertShows("example.com", "cloudflare.personal", "cached", "last Refresh 3h ago")

	h.press("j", "enter")
	h.assertShows("example.org", "cloudflare.work", "never Refreshed")
}

func TestMovingWithinRecordsPaneHighlightsTheRecord(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("enter", "tab", "tab")

	if !h.highlighted("192.0.2.10") || h.highlighted("2001:db8::10") {
		t.Fatalf("first Record should be highlighted:\n%q", h.rawView())
	}

	h.press("j")

	if h.highlighted("192.0.2.10") || !h.highlighted("2001:db8::10") {
		t.Fatalf("second Record should be highlighted:\n%q", h.rawView())
	}
}

func TestMovingInOnePaneLeavesOthersAlone(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("tab", "j", "shift+tab", "enter")

	h.assertShows("192.0.2.10")
}
