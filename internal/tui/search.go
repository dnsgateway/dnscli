package tui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// search is one pane's search filter.
type search struct {
	typing bool // the search input has the keyboard
	query  string
	caret  int // the input's caret, in runes back from the end
}

// startSearch opens the search input in the focused pane, if it has one and
// there is something to search.
func (m *Model) startSearch() {
	if m.searchable(m.focus) == 0 {
		return
	}

	m.search[m.focus].typing = true
}

// searchable returns how many items a pane's search looks through: all of
// its items before any filter. Panes without a search have none.
func (m *Model) searchable(p pane) int {
	switch p {
	case paneDomains:
		return len(m.zones)
	case paneRecords:
		return len(m.rows)
	}

	return 0
}

// clearSearch clears the focused pane's search filter and leaves search. It
// does nothing when the pane has no search, so the cursor stays put.
func (m *Model) clearSearch() {
	if m.search[m.focus] == (search{}) {
		return
	}

	m.search[m.focus] = search{}
	m.cursor[m.focus] = 0
	m.refilter(m.focus)
}

// typeIntoSearch sends a typed key to the focused pane's search input, and
// reports whether it did. See typeInto for which keys are typed.
func (m *Model) typeIntoSearch(msg tea.KeyMsg) bool {
	s := &m.search[m.focus]
	if m.help || !s.typing || !m.keys.typeInto(&s.query, &s.caret, msg) {
		return false
	}

	m.cursor[m.focus] = 0
	m.refilter(m.focus)

	return true
}

// typeInto edits a text input with a key at its caret, and reports whether
// it did. The caret counts runes back from the end of the text, so a text
// set anew has it at the end. Printable keys are typed, Backspace and
// Delete delete before and under the caret, Left and Right move it, and
// Home and End (or Ctrl+A and Ctrl+E) move it to the start and the end.
// Keys that are not text still reach actions, and so do Alt+key and the
// keys that leave the input: Close, Select, NextPane and PrevPane act even
// when bound to a printable key. Such a key moves the caret back to the
// end, so the field focused next starts there: every key the input does
// not take, such as Up or Tab, leaves it or closes it.
func (km keymap) typeInto(text *string, caret *int, msg tea.KeyMsg) bool {
	if !km.edit(text, caret, msg) {
		*caret = 0

		return false
	}

	return true
}

// edit is typeInto's editing, without moving the caret back to the end
// for a key it does not take.
func (km keymap) edit(text *string, caret *int, msg tea.KeyMsg) bool {
	if msg.Alt {
		return false
	}

	switch km.action(msg.String()) {
	case ActionClose, ActionSelect, ActionNextPane, ActionPrevPane:
		return false
	}

	r := []rune(*text)
	back := min(max(*caret, 0), len(r))
	at := len(r) - back

	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		r = slices.Concat(r[:at], msg.Runes, r[at:])
	case tea.KeyBackspace, tea.KeyCtrlH: // some terminals send ^H for Backspace
		if at > 0 {
			r = append(r[:at-1], r[at:]...)
		}
	case tea.KeyDelete:
		if back > 0 {
			r = append(r[:at], r[at+1:]...)
			back--
		}
	case tea.KeyLeft:
		back = min(back+1, len(r))
	case tea.KeyRight:
		back = max(back-1, 0)
	case tea.KeyHome, tea.KeyCtrlA:
		back = len(r)
	case tea.KeyEnd, tea.KeyCtrlE:
		back = 0
	default:
		return false
	}

	*text, *caret = string(r), back

	return true
}

// withCaret shows text with the caret, back runes from its end: the rune
// under it in reverse video, so the text does not move apart, or a bar
// after the text when the caret is at its end. A text wider than w columns
// shows the part around the caret that fits, with an ellipsis where it is
// cut.
func withCaret(text string, back, w int) string {
	r := []rune(text)
	back = min(max(back, 0), len(r))
	at := len(r) - back

	widths := make([]int, len(r))
	for i, c := range r {
		widths[i] = ansi.StringWidth(string(c))
	}

	// fits reports whether runes [from, to) fit in w columns.
	fits := func(from, to int) bool {
		n := 0
		for _, cw := range widths[from:to] {
			n += cw
		}

		return n <= w
	}

	// Show the end, or, when the caret is left of that, start just
	// before the caret, so it stays off the ellipsis.
	start := 0
	for !fits(start, len(r)) {
		start++
	}

	if at < start+1 {
		start = max(at-1, 0)
	}

	end := start
	for end < len(r) && fits(start, end+1) {
		end++
	}

	shown := slices.Clone(r[start:end])

	if start > 0 && len(shown) > 0 {
		shown[0] = '…'
	}

	if end < len(r) && len(shown) > 0 {
		shown[len(shown)-1] = '…'
	}

	at -= start
	if at >= len(shown) {
		return string(shown) + "▏"
	}

	return string(shown[:at]) + cursorStyle.Render(string(shown[at])) + string(shown[at+1:])
}

// searchLines renders a pane's search input: none while the pane has no
// search, and a "/query" line, with a cursor while typing, when it does.
func (m *Model) searchLines(p pane, w int) []string {
	s := m.search[p]
	if !s.typing && s.query == "" {
		return nil
	}

	if !s.typing {
		return []string{hintStyle.Render(fit(" /"+s.query, w))}
	}

	return []string{" /" + withCaret(s.query, s.caret, max(w-3, 1))}
}
