// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui_test

import "testing"

// Seeded Zones are listed by zone name: example.com, example.org, two.tv.

func TestRecordsPaneAsksToOpenAZoneBeforeOneIsOpened(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.assertShows("Press Space on a Zone")
	h.assertHides("192.0.2.10", "198.51.100.7")
}

func TestSpaceOnZoneShowsItsCachedRecords(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("space")

	h.assertShows("Name", "Type", "Content", "TTL")
	h.assertShows("192.0.2.10", "2001:db8::10", "www.example.com", "CNAME", "3600")
	h.assertHides("198.51.100.7", "Press Space on a Zone")
	h.assertDimmed("192.0.2.10")
}

func TestJAndDownMoveToLaterZones(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("j", "down", "space")

	h.assertShows("198.51.100.7")
	h.assertHides("192.0.2.10")
}

func TestKAndUpMoveToEarlierZones(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("j", "j", "k", "up", "space")

	h.assertShows("192.0.2.10")
}

func TestMovementStopsAtTheEndsOfTheList(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.press("k", "space")
	h.assertShows("192.0.2.10")

	// Down past the last Zone moves on to the Accounts pane, whose last
	// Account is as far as Down goes.
	h.press("j", "j", "space")
	h.assertShows("198.51.100.7")

	h.press("j", "j", "j", "j")
	h.assertFocused("Accounts")
	h.assertHighlighted("cloudflare.work")
}

func TestZoneWithoutCachedRecordsSaysSo(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("j", "space")

	h.assertShows("No cached Records")
}

func TestStatusBarShowsOpenZoneAndLastRefresh(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.press("space")
	h.assertShows("example.com", "cloudflare.personal", "cached", "last Refresh 3h ago")

	h.press("j", "space")
	h.assertShows("example.org", "cloudflare.work", "never Refreshed")
}

func TestMovingWithinRecordsPaneHighlightsTheRecord(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("space", "shift+tab", "shift+tab")

	if !h.highlighted("192.0.2.10") || h.highlighted("2001:db8::10") {
		t.Fatalf("first Record should be highlighted:\n%q", h.rawView())
	}

	h.press("j")

	if h.highlighted("192.0.2.10") || !h.highlighted("2001:db8::10") {
		t.Fatalf("second Record should be highlighted:\n%q", h.rawView())
	}
}

func TestRecordsPaneShowsLoadingWhileRecordsAreRead(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	pending := h.pressHolding("space")

	h.assertShows("loading")
	h.assertHides("No cached Records")

	h.run(pending)

	h.assertShows("192.0.2.10")
	h.assertHides("loading")
}

func TestRecordsReadFailureIsShownInTheRecordsPane(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	h.press("space")

	h.assertShows("Could not read Records", "list records")
	h.assertHides("No cached Records")
}

func TestMovingInOnePaneLeavesOthersAlone(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("shift+tab", "j", "tab", "space")

	h.assertShows("192.0.2.10")
}
