package tui_test

import (
	"testing"

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
		"Enter", "open / select",
		"q/Ctrl+C", "quit",
	)
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
