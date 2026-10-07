package tui_test

import "testing"

func TestEmptyDatabaseShowsAddFirstAccountPrompt(t *testing.T) {
	s, _ := openStore(t)
	h := newHarness(t, s)

	h.assertShows("Domains", "Accounts", "Records", "add your first Account")
}
