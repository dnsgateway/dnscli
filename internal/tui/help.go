package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var helpKeyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)

// helpView renders the help overlay from the current Shortcut Profile.
func (m *Model) helpView() string {
	keyW := 0
	for _, b := range m.keys.profile.Bindings {
		keyW = max(keyW, lipgloss.Width(m.keys.keysFor(b.Action)))
	}

	lines := []string{""}
	width := 0

	for _, b := range m.keys.profile.Bindings {
		k := m.keys.keysFor(b.Action)
		line := "  " + helpKeyStyle.Render(k) + strings.Repeat(" ", keyW-lipgloss.Width(k)) + "   " + b.Help + "  "
		lines = append(lines, line)
		width = max(width, lipgloss.Width(line))
	}

	lines = append(lines, "")

	return box("Help", lines, max(width+2, 24), len(lines)+2, true)
}

// overlay draws fg centred on top of bg.
func overlay(bg, fg string, width, height int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")
	fgW := lipgloss.Width(fg)

	x := max((width-fgW)/2, 0)
	y := max((height-len(fgLines))/2, 0)

	for i, fl := range fgLines {
		row := y + i
		if row >= len(bgLines) {
			break
		}

		bl := bgLines[row]
		left := ansi.Truncate(bl, x, "")
		left += strings.Repeat(" ", max(x-lipgloss.Width(left), 0))
		right := ansi.TruncateLeft(bl, x+fgW, "")

		bgLines[row] = left + fl + right
	}

	return strings.Join(bgLines, "\n")
}
