package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/tui"
)

func TestHelpOverlayListsTheCurrentKeys(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.assertHides("next pane")

	h.press("?")

	h.assertShows("Help",
		"Tab", "next pane",
		"Shift+Tab", "previous pane",
		"↑/k", "move up",
		"↓/j", "move down",
		"q/Ctrl+C", "quit",
	)
}

func TestHelpOverlayListsTheFocusedPanesKeysBelowTheGlobalOnes(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.press("?")
	h.assertShows("── Domains ─", "show the Zone's Records")
	h.assertHides("── Records ─", "── Accounts ─", "insert a new Record", "add an Account")

	h.press("esc", "shift+tab", "?")
	h.assertShows("── Accounts ─", "show only the Account's Zones", "add an Account", "edit the Account", "remove the Account", "re-list")
	h.assertHides("── Domains ─", "insert a new Record")
	// Edit and Select edit alike: one line lists both keys.
	h.assertShows("e/Enter")

	if n := strings.Count(h.view(), "edit the Account"); n != 1 {
		t.Fatalf("edit is listed %d times, want once:\n%s", n, h.view())
	}

	// The global keys show in every pane, above the rule.
	h.assertShows("next pane", "search", "quit")
	h.assertHides("Delegation")

	view := h.view()
	if strings.Index(view, "quit") > strings.Index(view, "── Accounts ─") {
		t.Fatalf("the global keys are not above the pane's:\n%s", view)
	}
}

func TestEscClosesHelpOverlay(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("?", "esc")

	h.assertHides("next pane")
	h.assertShows("example.com")
}

func TestHelpKeyTogglesHelpOverlay(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("?", "?")

	h.assertHides("next pane")
}

func TestHelpOverlayReflectsTheShortcutProfile(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	profile := tui.DefaultShortcutProfile()
	for i, b := range profile.Bindings {
		if b.Action == tui.ActionNextPane {
			profile.Bindings[i].Keys = []string{"ctrl+n"}
		}
	}

	h := newHarnessWith(t, s, tui.Deps{Shortcuts: &profile})
	h.press("?")

	h.assertShows("ctrl+n", "next pane")
}

func TestHelpOverlayCapturesNavigationKeys(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("?", "tab", "enter", "esc")

	h.assertFocused("Domains")
	h.assertHides("192.0.2.10")
}

func TestWithoutAccountsHelpSaysInsertAddsOneFromAnyPane(t *testing.T) {
	s, _ := openStore(t)
	h := newHarness(t, s)

	for _, keys := range [][]string{{"?"}, {"esc", "tab", "?"}} {
		h.press(keys...)
		h.assertShows("i/insert", "add an Account")
		h.assertHides("insert a new Record")
	}
}

func TestHelpInTheDomainsPaneSaysWhatTheZoneIconsMean(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 50})

	h.press("?")
	h.assertShows("Domains icons", "±", "pending changes", "the Live Zone", "registration only", "serial differs: maybe stale", "a Stale Zone")

	// Other panes' help has no legend.
	h.press("esc", "shift+tab", "?")
	h.assertHides("Domains icons")
}

func TestHelpLeavesTheIconLegendOutWhenTheTerminalIsTooShort(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("?")

	h.assertShows("quit", "show the Zone's Records")
	h.assertHides("Domains icons")
}

func TestPreviewKeysAreListedOnlyInTheRecordsPanesHelp(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("?")
	h.assertHides("preview the pending changes", "discard the pending changes", "apply over the current values")

	h.press("esc", "right", "?")
	h.assertShows("preview the pending changes", "discard the pending changes", "apply over the current values")
}

func TestPreviewingOutsideTheRecordsPaneSaysWhereItWorks(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("space", "P")

	h.assertShows("Pending changes are previewed from the Records pane")
}
