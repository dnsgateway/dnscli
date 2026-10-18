// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui_test

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestATypedFieldEditsAtItsCaret(t *testing.T) {
	s, _ := openStore(t)
	h := newAccountHarness(t, s, newFake())

	h.press("i", "down")
	h.typeText("work")
	h.assertShows("work▏")

	// Left and Right move the caret; typing inserts at it.
	h.press("left", "left")
	h.assertCaret("wo", "r", "k")
	h.typeText("X")
	h.assertCaret("woX", "r", "k")
	h.press("right")
	h.assertCaret("woXr", "k", "")

	// Backspace deletes before the caret, Delete under it.
	h.press("backspace", "delete")
	h.assertShows("woX▏")

	// Home and End move to the start and the end, and so do Ctrl+A and Ctrl+E.
	h.press("home")
	h.typeText("A")
	h.assertCaret("A", "w", "oX")
	h.press("end")
	h.typeText("Z")
	h.assertShows("AwoXZ▏")
	h.press("ctrl+a")
	h.assertCaret("", "A", "woXZ")
	h.press("ctrl+e")
	h.assertShows("AwoXZ▏")

	// The caret stops at the ends.
	h.press("right", "home", "left", "delete")
	h.assertCaret("", "w", "oXZ")

	// Another field starts with its caret at the end.
	h.press("down")
	h.typeText("tok")
	h.press("left", "up")
	h.assertShows("woXZ▏")
	h.press("down")
	h.assertShows("•••▏")
}

func TestALongValueKeepsItsCaretInView(t *testing.T) {
	s, _ := openStore(t)
	h := newAccountHarness(t, s, newFake())

	h.press("i", "down")
	h.typeText("start" + strings.Repeat("m", 60) + "finish")
	h.assertShows("finish▏")
	h.assertHides("start")

	h.press("home")
	h.assertCaret("", "s", "tart")
	h.assertHides("finish")
	h.assertDialogFits("Add Account")
}

func TestTheSearchInputEditsAtItsCaret(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("/")
	h.typeText("exampl.org")
	h.press("left", "left", "left", "left")
	h.typeText("e")

	h.assertCaret("/example", ".", "org")
	h.assertHides("example.com", "two.tv")
	h.assertFocused("Domains")
}

// assertCaret checks that a text field shows before+under+after with the
// caret on under: drawn in reverse video, without moving the text apart.
func (h *harness) assertCaret(before, under, after string) {
	h.t.Helper()

	h.assertShows(before + under + after)

	caret := regexp.MustCompile(regexp.QuoteMeta(before) + `\x1b\[[0-9;]*7[0-9;]*m` + regexp.QuoteMeta(under) + `\x1b\[[0-9;]*m` + regexp.QuoteMeta(after))
	if !caret.MatchString(h.rawView()) {
		h.t.Fatalf("the caret is not on %q in %q:\n%q", under, before+under+after, h.rawView())
	}
}

func TestLeavingAFieldMovesItsCaretBackToTheEnd(t *testing.T) {
	s, _ := openStore(t)
	h := newAccountHarness(t, s, newFake())

	h.press("i", "down")
	h.typeText("work")
	h.press("left", "left")

	// Down to the API token and back, typing nothing there.
	h.press("down", "up")
	h.assertShows("work▏")
}

func TestAFixedFieldShowsNoCaret(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newAccountHarness(t, s, newFake())

	h.press("shift+tab", "e")
	h.unsealWith(passphrase)
	h.press("up")

	h.assertShows("› Provider   cloudflare")
	h.assertHides("cloudflare▏", "‹ cloudflare ›")
}

func TestWideCharactersKeepTheDialogInsideItsSize(t *testing.T) {
	s, _ := openStore(t)
	h := newAccountHarness(t, s, newFake())

	h.press("i")
	w, ht := h.dialogSize("Add Account")

	h.press("down")
	h.typeText(strings.Repeat("名", 60))
	h.press("left", "left")

	if gw, gh := h.dialogSize("Add Account"); gw != w || gh != ht {
		t.Fatalf("wide characters resized the dialog from %dx%d to %dx%d:\n%s", w, ht, gw, gh, h.view())
	}

	h.assertCaret("名", "名", "名")
}

func TestTypingDoesNotChangeTheKeyMessage(t *testing.T) {
	s, _ := openStore(t)
	h := newAccountHarness(t, s, newFake())

	h.press("i", "down")
	h.typeText("work")
	h.press("left", "left")

	// A pasted key whose runes have spare room after them.
	runes := make([]rune, 2, 16)
	copy(runes, []rune("XY"))
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: runes})

	if got := string(runes[:cap(runes)][:4]); got != "XY\x00\x00" {
		t.Fatalf("the key message's runes became %q", got)
	}

	h.assertCaret("woXY", "r", "k")
}
