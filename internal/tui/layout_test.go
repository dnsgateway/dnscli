// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// assertFits checks that the view is no larger than a w×h terminal.
func (h *harness) assertFits(w, ht int) {
	h.t.Helper()

	lines := strings.Split(h.rawView(), "\n")
	if len(lines) > ht {
		h.t.Fatalf("view is %d lines high in a %dx%d terminal:\n%s", len(lines), w, ht, h.view())
	}

	for i, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			h.t.Fatalf("line %d is %d cells wide in a %dx%d terminal:\n%s", i, lw, w, ht, h.view())
		}
	}
}

func TestPaneScrollsToKeepTheCursorVisible(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 6}) // the Domains pane has room for one row

	h.assertShows("example.com")
	h.assertHides("example.org", "two.tv")

	h.press("j", "j")

	h.assertShows("two.tv")
	h.assertHides("example.com", "example.org")
}

func TestPaneScrollsUpOnlyPastItsFirstRowShown(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 9}) // the Domains pane has room for two rows

	h.press("j", "j")
	h.assertShows("example.org", "two.tv")
	h.assertHides("example.com")

	// Up within the rows shown moves the cursor, not the list.
	h.press("k")
	h.assertShows("example.org", "two.tv")
	h.assertHides("example.com")

	// Up past the first row shown scrolls the list one row.
	h.press("k")
	h.assertShows("example.com", "example.org")
	h.assertHides("two.tv")
}

func TestViewFitsSmallTerminals(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 8}, {20, 4}, {10, 3}, {1, 1}} {
		w, ht := size[0], size[1]

		t.Run(fmt.Sprintf("%dx%d", w, ht), func(t *testing.T) {
			s, _ := openStore(t)
			seedCache(t, s)

			h := newHarness(t, s)
			h.press("space")
			h.send(tea.WindowSizeMsg{Width: w, Height: ht})

			h.assertFits(w, ht)

			h.press("?")

			h.assertFits(w, ht)
		})
	}
}
