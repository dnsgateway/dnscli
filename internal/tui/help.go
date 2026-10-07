package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var helpKeyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)

// helpView renders the help overlay from the current Shortcut Profile: the
// global keys, then, below a rule, the keys of the focused pane.
func (m *Model) helpView() string {
	type row struct{ keys, help string }

	var global, local []row

	for _, b := range m.keys.profile.Bindings {
		if !paneAction(b.Action) {
			global = append(global, row{m.keys.keysFor(b.Action), b.Help})
		}
	}

	for _, b := range m.paneBindings() {
		if k := m.keys.keysFor(b.Action); k != "" {
			local = append(local, row{k, b.Help})
		}
	}

	keyW := 0
	for _, r := range append(global, local...) {
		keyW = max(keyW, lipgloss.Width(r.keys))
	}

	render := func(rows []row) []string {
		lines := make([]string, 0, len(rows))
		for _, r := range rows {
			lines = append(lines, "  "+helpKeyStyle.Render(r.keys)+strings.Repeat(" ", keyW-lipgloss.Width(r.keys))+"   "+r.help+"  ")
		}

		return lines
	}

	lines := append([]string{""}, render(global)...)
	pane := render(local)

	width := 0
	for _, l := range append(lines, pane...) {
		width = max(width, lipgloss.Width(l))
	}

	if len(pane) > 0 {
		title := " " + paneNames[m.focus] + " "
		rule := " ──" + title + strings.Repeat("─", max(width-4-lipgloss.Width(title), 0)) + " "
		lines = append(lines, "", hintStyle.Render(rule))
		lines = append(lines, pane...)
	}

	lines = append(lines, "")

	return box("Help", lines, max(width+2, 24), len(lines)+2, true)
}

// paneBindings are the focused pane's Actions for the help overlay.
// Without Accounts, insert adds one from any pane.
func (m *Model) paneBindings() []paneBinding {
	bs := paneHelp[m.focus]
	if len(m.accounts) > 0 {
		return bs
	}

	out := []paneBinding{{ActionInsert, "add an Account"}}

	for _, b := range bs {
		if b.Action != ActionInsert {
			out = append(out, b)
		}
	}

	return out
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
		// Cut without an ellipsis: the cut edge sits against the overlay's border.
		left := fit(ansi.Truncate(bl, x, ""), x)
		right := ansi.TruncateLeft(bl, x+fgW, "")

		bgLines[row] = left + fl + right
	}

	return strings.Join(bgLines, "\n")
}
