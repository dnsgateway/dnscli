package tui_test

import (
	"testing"

	"github.com/sergelogvinov/dnscli/internal/tui"
)

// profileWith returns the default Shortcut Profile with action bound to keys,
// or with action removed when keys is nil.
func profileWith(action tui.Action, keys ...string) tui.ShortcutProfile {
	p := tui.DefaultShortcutProfile()
	p.Name = "custom"

	var bindings []tui.Binding

	for _, b := range p.Bindings {
		if b.Action == action {
			if keys == nil {
				continue
			}

			b.Keys = keys
		}

		bindings = append(bindings, b)
	}

	p.Bindings = bindings

	return p
}

func TestShortcutProfileWithoutQuitIsRejected(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	profile := profileWith(tui.ActionQuit)

	h := newHarnessWith(t, s, tui.Deps{Shortcuts: &profile})
	h.assertShows("Shortcut Profile custom", "no quit key", "using the default")

	h.press("q")

	if !h.quit {
		t.Fatal("the default quit key did not quit")
	}
}

func TestShortcutProfileWithAKeyBoundTwiceIsRejected(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	profile := profileWith(tui.ActionNextPane, "j")

	h := newHarnessWith(t, s, tui.Deps{Shortcuts: &profile})
	h.assertShows("Shortcut Profile custom", `"j"`, "using the default")

	h.press("tab")

	h.assertFocused("Records")
}

func TestValidShortcutProfileIsUsed(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	profile := profileWith(tui.ActionNextPane, "n")

	h := newHarnessWith(t, s, tui.Deps{Shortcuts: &profile})
	h.assertHides("Shortcut Profile")

	h.press("n")

	h.assertFocused("Records")
}
