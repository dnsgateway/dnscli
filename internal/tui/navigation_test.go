package tui_test

import (
	"strings"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/tui"
)

// focused returns the titles of panes drawn with the heavy focus border.
func (h *harness) focused() []string {
	var out []string

	for _, title := range []string{"Domains", "Accounts", "Records"} {
		if strings.Contains(h.view(), "┏━ "+title+" ") {
			out = append(out, title)
		}
	}

	return out
}

func (h *harness) assertFocused(title string) {
	h.t.Helper()

	got := h.focused()
	if len(got) != 1 || got[0] != title {
		h.t.Fatalf("focused panes = %v, want [%s]:\n%s", got, title, h.view())
	}
}

func TestDomainsPaneIsFocusedAtStartup(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.assertFocused("Domains")
}

func TestTabCyclesFocusForward(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.press("tab")
	h.assertFocused("Accounts")

	h.press("tab")
	h.assertFocused("Records")

	h.press("tab")
	h.assertFocused("Domains")
}

func TestShiftTabCyclesFocusBackward(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.press("shift+tab")
	h.assertFocused("Records")

	h.press("shift+tab")
	h.assertFocused("Accounts")

	h.press("shift+tab")
	h.assertFocused("Domains")
}

func TestKeysComeFromTheShortcutProfile(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	profile := tui.DefaultShortcutProfile()
	for i, b := range profile.Bindings {
		if b.Action == tui.ActionNextPane {
			profile.Bindings[i].Keys = []string{"n"}
		}
	}

	h := newHarnessWith(t, s, tui.Deps{Shortcuts: &profile})

	h.press("tab")
	h.assertFocused("Domains")

	h.press("n")
	h.assertFocused("Accounts")
}
