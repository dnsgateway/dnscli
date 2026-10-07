package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// quitsDialog reports whether a key quits the app while a dialog is open.
// Only non-printable quit keys do, such as Ctrl+C: printable ones may be
// part of what the user types.
func quitsDialog(a Action, msg tea.KeyMsg) bool {
	return a == ActionQuit && msg.Type != tea.KeyRunes && msg.Type != tea.KeySpace
}

// dialogField is one row of a dialog form.
type dialogField struct {
	label  string
	value  string
	masked bool // a secret: shown as dots
	choice bool // picked with Up/Down instead of typed
}

// dialogView renders a dialog box: its fields, with the focused one marked,
// followed by free text lines (notes, warnings, the error).
func dialogView(title string, fields []dialogField, focus int, typing bool, notes []string) string {
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
	lines = append(lines, "")

	width := 48
	for _, l := range lines {
		width = max(width, lipgloss.Width(l)+2)
	}

	for i, l := range lines {
		lines[i] = " " + l
	}

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
