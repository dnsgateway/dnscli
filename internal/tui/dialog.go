package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// quitsDialog reports whether a key quits the app while a dialog is open.
// Only non-printable quit keys do, such as Ctrl+C: printable ones may be
// part of what the user types.
func quitsDialog(a Action, msg tea.KeyMsg) bool {
	return a == ActionQuit && msg.Type != tea.KeyRunes && msg.Type != tea.KeySpace
}

// Dialogs stay narrow enough for an 80-column terminal: notes wrap at
// noteWidth, and a long value shows only its end.
const (
	noteWidth     = 56
	maxValueWidth = 40
)

// dialogField is one row of a dialog form.
type dialogField struct {
	label  string
	value  string
	masked bool // a secret: shown as dots
	choice bool // picked from a list of values instead of typed
}

// dialogView renders a dialog box: its fields, with the focused one marked,
// followed by free text lines (notes, warnings, the error).
func dialogView(title string, fields []dialogField, focus int, typing bool, notes []string) string {
	return sizedDialogView(title, fields, focus, typing, notes, nil, dialogSize{})
}

// dialogSize is the outer size of a dialog box, borders included.
type dialogSize struct{ w, h int }

// grow returns the size that holds both s and the rendered dialog d.
func (s dialogSize) grow(d string) dialogSize {
	return dialogSize{max(s.w, lipgloss.Width(d)), max(s.h, lipgloss.Height(d))}
}

// sizedDialogView is dialogView in a box at least least in size, so that
// a dialog whose content changes keeps one size, with footer lines, such
// as hints, centered at the bottom of the box.
func sizedDialogView(title string, fields []dialogField, focus int, typing bool, notes, footer []string, least dialogSize) string {
	labelW := 0
	for _, f := range fields {
		labelW = max(labelW, lipgloss.Width(f.label))
	}

	lines := []string{""}

	for i, f := range fields {
		v := f.value
		if f.masked {
			v = strings.Repeat("•", len([]rune(v)))
		}

		if w := lipgloss.Width(v); w > maxValueWidth {
			v = "…" + ansi.TruncateLeft(v, w-maxValueWidth+1, "")
		}

		marker := "  "

		if i == focus {
			marker = "› "

			switch {
			case f.choice:
				v = "‹ " + v + " ›"
			case typing:
				v += "▏"
			}
		}

		lines = append(lines, marker+helpKeyStyle.Render(f.label)+strings.Repeat(" ", labelW-lipgloss.Width(f.label))+"  "+v)
	}

	lines = append(lines, "")
	lines = append(lines, notes...)

	if len(footer) > 0 {
		lines = append(lines, "")
	}

	for len(lines)+len(footer)+3 < least.h {
		lines = append(lines, "")
	}

	width := max(48, least.w-2)
	for _, l := range append(lines, footer...) {
		width = max(width, lipgloss.Width(l)+2)
	}

	for i, l := range lines {
		lines[i] = " " + l
	}

	for _, l := range footer {
		lines = append(lines, strings.Repeat(" ", (width-lipgloss.Width(l))/2)+l)
	}

	lines = append(lines, "")

	return box(title, lines, width+2, len(lines)+2, true)
}

// wrapNote splits a note into lines of at most w columns, each indented.
func wrapNote(style lipgloss.Style, text string, w int) []string {
	var lines []string

	line := ""

	for _, word := range strings.Fields(text) {
		if line != "" && lipgloss.Width(line)+1+lipgloss.Width(word) > w {
			lines = append(lines, style.Render(line))
			line = ""
		}

		if line != "" {
			line += " "
		}

		line += word
	}

	if line != "" {
		lines = append(lines, style.Render(line))
	}

	return lines
}

// picker is a popup over a form that lists every value of one option
// field, such as the Record types, to pick one with Up and Down.
type picker struct {
	title   string
	options []string
	cursor  int
	// pick sets the field to its i-th value.
	pick func(i int)
}

// key handles a key while the picker is open: Up and Down move through
// the values, Enter picks one and Esc keeps the field as it was. It
// reports whether the picker closed.
func (p *picker) key(a Action) bool {
	switch a {
	case ActionUp:
		p.cursor = max(p.cursor-1, 0)
	case ActionDown:
		p.cursor = min(p.cursor+1, len(p.options)-1)
	case ActionSelect:
		p.pick(p.cursor)

		return true
	case ActionClose:
		return true
	}

	return false
}

// view renders the picker in at most h rows: every value of the field,
// the highlighted one marked with › in reverse video.
func (p *picker) view(h int) string {
	width := lipgloss.Width(p.title) + 6
	for _, o := range p.options {
		width = max(width, lipgloss.Width(o)+6)
	}

	start, end := window(len(p.options), p.cursor, max(h-2, 1))

	lines := make([]string, 0, end-start)

	for i := start; i < end; i++ {
		line := "  " + fit(p.options[i], width-4)
		if i == p.cursor {
			line = cursorStyle.Render("› " + fit(p.options[i], width-4))
		}

		lines = append(lines, line)
	}

	return box(p.title, lines, width, len(lines)+2, true)
}

// saveRow renders a form's Save button, marked when focused.
func saveRow(focused bool) string {
	if focused {
		return "› " + cursorStyle.Render("[ Save ]")
	}

	return "  [ Save ]"
}

// formKeysHint says how to move through a form with option fields and a
// Save button, for the status line.
func (m *Model) formKeysHint() string {
	k := m.keys.keysFor

	return k(ActionUp) + " " + k(ActionDown) + " field · " + k(ActionPaneLeft) + " " + k(ActionPaneRight) + " change · " +
		k(ActionSelect) + " list or save · " + k(ActionClose) + " cancel"
}
