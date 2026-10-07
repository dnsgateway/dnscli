package tui_test

import "testing"

// Seeded Accounts are listed as cloudflare.personal, cloudflare.work.
// cloudflare.personal hosts example.com and two.tv; cloudflare.work hosts example.org.

func TestSelectingAnAccountShowsOnlyItsZones(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("tab", "enter")

	h.assertShows("example.com", "two.tv")
	h.assertHides("example.org")

	h.press("j", "enter")

	h.assertShows("example.org")
	h.assertHides("example.com", "two.tv")
}
