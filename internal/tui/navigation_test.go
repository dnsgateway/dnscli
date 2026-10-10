package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
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
	h.assertFocused("Records")

	h.press("tab")
	h.assertFocused("Accounts")

	h.press("tab")
	h.assertFocused("Domains")
}

func TestShiftTabCyclesFocusBackward(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.press("shift+tab")
	h.assertFocused("Accounts")

	h.press("shift+tab")
	h.assertFocused("Records")

	h.press("shift+tab")
	h.assertFocused("Domains")
}

func TestRightAndLeftArrowsMoveBetweenTheDomainsAndRecordsPanes(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.press("right")
	h.assertFocused("Records")

	h.press("right") // nothing is right of the Records pane
	h.assertFocused("Records")

	h.press("left")
	h.assertFocused("Domains")

	h.press("left") // nothing is left of the Domains pane
	h.assertFocused("Domains")

	// The Accounts pane is on the left too.
	h.press("shift+tab", "right")
	h.assertFocused("Records")
}

func TestDownPastTheLastZoneMovesToAccountsAndUpPastTheFirstAccountMovesBack(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.press("down", "down") // two.tv, the last Zone
	h.assertFocused("Domains")

	h.press("down")
	h.assertFocused("Accounts")
	h.assertHighlighted("cloudflare.personal")

	h.press("up")
	h.assertFocused("Domains")
	h.assertHighlighted("two.tv")

	// Inside the lists, Up and Down only move the cursor.
	h.press("up")
	h.assertFocused("Domains")
	h.assertHighlighted("example.org")
}

func TestUpAtTheFirstAccountReturnsToTheCurrentZone(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)

	h.press("down", "shift+tab") // example.org, then the Accounts pane
	h.press("down", "up")        // the first Account again
	h.assertFocused("Accounts")

	h.press("up")
	h.assertFocused("Domains")
	h.assertHighlighted("example.org")
}

func TestPageDownAndPageUpScrollTheFocusedListAPageAtATime(t *testing.T) {
	var records []dns.Record
	for i := range 30 {
		records = append(records, dns.Record{Type: "A", Name: fmt.Sprintf("h%02d.mail.example", i), Value: "192.0.2.1", TTL: 300})
	}

	h := openCachedZone(t, records...)
	// 12 rows: the Zone Status bar, the status line, the borders and the
	// header leave the Records pane 7 rows of Records.
	h.send(tea.WindowSizeMsg{Width: 120, Height: 12})
	h.press("right")

	h.press("pgdown")
	h.assertHighlighted("h07.mail.example")
	h.assertHides("h00.mail.example")

	h.press("pgdown", "pgdown", "pgdown", "pgdown") // stops at the last Record
	h.assertHighlighted("h29.mail.example")

	h.press("pgup")
	h.assertHighlighted("h22.mail.example")
	h.assertHides("h29.mail.example")

	h.press("pgup", "pgup", "pgup", "pgup") // stops at the first Record
	h.assertHighlighted("h00.mail.example")
}

func TestPagingKeepsTypingInTheSearch(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("/", "e", "pgdown", "x")

	h.assertShows("/ex")
}

func (h *harness) assertHighlighted(text string) {
	h.t.Helper()

	if !h.highlighted(text) {
		h.t.Fatalf("%q is not the cursor row:\n%s", text, h.view())
	}
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
	h.assertFocused("Records")
}
