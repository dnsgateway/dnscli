package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

var (
	borderStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	focusBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	headerStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	hintStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	statusStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	// staleStyle dims cached data that has not been confirmed in this session.
	staleStyle = lipgloss.NewStyle().Faint(true)
)

// View renders the three panes and the status bar.
func (m *Model) View() string {
	leftW := max(m.width/4, 24)
	rightW := max(m.width-leftW, 20)
	bodyH := max(m.height-1, 6)
	domainsH := max(bodyH*2/3, 3)
	accountsH := max(bodyH-domainsH, 3)

	left := lipgloss.JoinVertical(lipgloss.Left,
		box("Domains", m.domainLines(leftW-2, domainsH-2), leftW, domainsH, m.focus == paneDomains),
		box("Accounts", m.accountLines(leftW-2, accountsH-2), leftW, accountsH, m.focus == paneAccounts),
	)
	right := box("Records", m.recordLines(rightW-2, bodyH-2), rightW, bodyH, m.focus == paneRecords)

	view := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, left, right),
		ansi.Truncate(m.statusLine(), m.width, "…"),
	)

	if m.help {
		view = overlay(view, m.helpView(), m.width, m.height)
	}

	return view
}

func (m *Model) domainLines(w, h int) []string {
	lines := make([]string, len(m.zones))

	for i, z := range m.zones {
		// Every Zone is cached and unconfirmed until freshness lands, so all are dimmed.
		lines[i] = m.row(paneDomains, i, " "+z.Name, w, staleStyle)
	}

	return scroll(lines, m.cursor[paneDomains], h)
}

func (m *Model) accountLines(w, h int) []string {
	lines := make([]string, len(m.accounts))

	for i, a := range m.accounts {
		lines[i] = m.row(paneAccounts, i, " "+a.Label(), w, lipgloss.NewStyle())
	}

	return scroll(lines, m.cursor[paneAccounts], h)
}

func (m *Model) recordLines(w, h int) []string {
	switch {
	case !m.loaded:
		return nil
	case len(m.accounts) == 0 && m.err != nil:
		return []string{"", errorStyle.Render("  Could not read the local database.")}
	case len(m.accounts) == 0:
		return []string{
			"",
			"  No Accounts yet: add your first Account to get started.",
			"",
			"  An Account is a Provider, a name and an API token.",
			hintStyle.Render("  dnscli keeps Accounts in its local database; it is not a disposable cache."),
		}
	case m.open == nil:
		return []string{"", fmt.Sprintf("  Press %s on a Zone to see its cached Records.", m.keys.keysFor(ActionSelect))}
	case len(m.records) == 0:
		return []string{"", "  No cached Records for " + m.open.Name + "."}
	}

	cols := recordColumns(w)
	header := headerStyle.Render(cols.format("Name", "Type", "Content", "TTL"))

	lines := make([]string, len(m.records))
	for i, r := range m.records {
		lines[i] = m.row(paneRecords, i, cols.format(r.Name, r.Type, r.Value, strconv.FormatUint(uint64(r.TTL), 10)), w, staleStyle)
	}

	return append([]string{header}, scroll(lines, m.cursor[paneRecords], h-1)...)
}

// row renders one list item. The cursor row is drawn in reverse video in
// the focused pane, and marked with › elsewhere.
func (m *Model) row(p pane, i int, text string, w int, style lipgloss.Style) string {
	if i == m.cursor[p] {
		if m.focus == p {
			style = style.Reverse(true)
		}

		text = "›" + strings.TrimPrefix(text, " ")
	}

	text = ansi.Truncate(text, w, "…")
	text += strings.Repeat(" ", max(w-lipgloss.Width(text), 0))

	return style.Render(text)
}

type columns struct{ name, typ, ttl, content int }

func recordColumns(w int) columns {
	c := columns{typ: 6, ttl: 6}
	rest := max(w-1-c.typ-c.ttl-3, 10)
	c.name = rest * 2 / 5
	c.content = rest - c.name

	return c
}

func (c columns) format(name, typ, content, ttl string) string {
	cell := func(s string, w int) string {
		s = ansi.Truncate(s, w, "…")

		return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
	}

	return " " + cell(name, c.name) + " " + cell(typ, c.typ) + " " + cell(content, c.content) + " " + cell(ttl, c.ttl)
}

// scroll returns the window of lines of height h that keeps cursor visible.
func scroll(lines []string, cursor, h int) []string {
	if h <= 0 || len(lines) <= h {
		return lines
	}

	start := min(max(cursor-h+1, 0), len(lines)-h)

	return lines[start : start+h]
}

func (m *Model) statusLine() string {
	switch {
	case m.err != nil:
		return errorStyle.Render(" " + m.err.Error())
	case !m.loaded:
		return statusStyle.Render(" loading cache…")
	case m.open != nil:
		return statusStyle.Render(" " + m.zoneStatus(*m.open))
	}

	return hintStyle.Render(fmt.Sprintf(" %s help · %s quit", m.keys.keysFor(ActionHelp), m.keys.keysFor(ActionQuit)))
}

func (m *Model) zoneStatus(z dns.Zone) string {
	parts := []string{z.Name}

	if a, ok := m.account(z.AccountID); ok {
		parts = append(parts, a.Label())
	}

	if z.RefreshedAt.IsZero() {
		parts = append(parts, "cached, never Refreshed")
	} else {
		parts = append(parts, "cached, last Refresh "+ago(m.deps.now().Sub(z.RefreshedAt)))
	}

	return strings.Join(parts, " · ")
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}

	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// box draws a bordered pane of outer size w×h with the title in the top
// border. The focused pane uses a heavy, coloured border so it stands out.
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
		line += strings.Repeat(" ", max(innerW-lipgloss.Width(line), 0))

		sb.WriteString("\n" + style.Render(b.Left) + line + style.Render(b.Right))
	}

	sb.WriteString("\n" + style.Render(b.BottomLeft+strings.Repeat(b.Bottom, innerW)+b.BottomRight))

	return sb.String()
}
