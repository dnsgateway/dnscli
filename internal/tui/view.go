package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	borderStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	focusBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	hintStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	statusStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	staleStyle       = lipgloss.NewStyle().Faint(true)
)

// View renders the three panes and the status bar.
func (m *Model) View() string {
	leftW := max(m.width/4, 24)
	rightW := max(m.width-leftW, 20)
	bodyH := max(m.height-1, 6)
	domainsH := max(bodyH*2/3, 3)
	accountsH := max(bodyH-domainsH, 3)

	left := lipgloss.JoinVertical(lipgloss.Left,
		box("Domains", m.domainLines(), leftW, domainsH, false),
		box("Accounts", m.accountLines(), leftW, accountsH, false),
	)
	right := box("Records", m.recordLines(), rightW, bodyH, false)

	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, left, right),
		m.statusLine(),
	)
}

func (m *Model) domainLines() []string {
	lines := make([]string, 0, len(m.zones))

	for _, z := range m.zones {
		// Every Zone is cached and unconfirmed until freshness lands, so all are dimmed.
		lines = append(lines, staleStyle.Render(" "+z.Name))
	}

	return lines
}

func (m *Model) accountLines() []string {
	lines := make([]string, 0, len(m.accounts))

	for _, a := range m.accounts {
		lines = append(lines, " "+a.Label())
	}

	return lines
}

func (m *Model) recordLines() []string {
	if m.loaded && len(m.accounts) == 0 {
		return []string{
			"",
			"  No Accounts yet: add your first Account to get started.",
			"",
			"  An Account is a Provider, a name and an API token.",
			hintStyle.Render("  (dnscli keeps Accounts in its local database; it is not a disposable cache.)"),
		}
	}

	return nil
}

func (m *Model) statusLine() string {
	if m.err != nil {
		return errorStyle.Render(" " + m.err.Error())
	}

	if !m.loaded {
		return statusStyle.Render(" loading cache…")
	}

	return ""
}

// box draws a bordered pane of outer size w×h with the title in the top
// border. The focused pane uses a heavy border so it stands out.
func box(title string, lines []string, w, h int, focused bool) string {
	b := lipgloss.NormalBorder()
	style := borderStyle

	if focused {
		b = lipgloss.ThickBorder()
		style = focusBorderStyle
	}

	innerW := max(w-2, 1)
	innerH := max(h-2, 1)

	label := " " + title + " "
	fill := max(innerW-1-lipgloss.Width(label), 0)

	var sb strings.Builder

	sb.WriteString(style.Render(b.TopLeft + b.Top + label + strings.Repeat(b.Top, fill) + b.TopRight))

	for i := range innerH {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}

		line = ansi.Truncate(line, innerW, "…")
		line += strings.Repeat(" ", innerW-lipgloss.Width(line))

		sb.WriteString("\n" + style.Render(b.Left) + line + style.Render(b.Right))
	}

	sb.WriteString("\n" + style.Render(b.BottomLeft+strings.Repeat(b.Bottom, innerW)+b.BottomRight))

	return sb.String()
}
