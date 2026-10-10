package tui_test

import (
	"testing"

	"github.com/sergelogvinov/dnscli/internal/tui"
)

// rootDone reports whether the model's root context, from which all
// background work derives, has been canceled.
func (h *harness) rootDone() bool {
	return h.model.(*tui.Model).Context().Err() != nil
}

func TestQuitCancelsBackgroundWork(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		t.Run(key, func(t *testing.T) {
			s, _ := openStore(t)
			seedCache(t, s)

			h := newHarness(t, s)
			if h.rootDone() {
				t.Fatal("root context canceled before quitting")
			}

			h.press(key)

			if !h.quit {
				t.Fatal("app did not quit")
			}

			if !h.rootDone() {
				t.Fatal("quitting did not cancel the root context")
			}
		})
	}
}

func TestQuitWorksFromTheHelpOverlay(t *testing.T) {
	s, _ := openStore(t)

	h := newHarness(t, s)
	h.press("?", "ctrl+c")

	if !h.quit || !h.rootDone() {
		t.Fatal("ctrl+c in the help overlay did not quit and cancel background work")
	}
}

func TestOtherKeysDoNotQuit(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("shift+tab", "j", "space", "esc", "x")

	if h.quit || h.rootDone() {
		t.Fatal("app quit on a non-quit key")
	}
}
