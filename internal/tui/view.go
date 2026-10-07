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
	sealedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	// staleStyle dims cached data that has not been confirmed in this session.
	staleStyle = lipgloss.NewStyle().Faint(true)
)

// View renders the three panes and the status bar.
func (m *Model) View() string {
	// Panes keep their minimum sizes while the terminal has room for them,
	// and shrink to fit it when it does not.
	leftW := max(m.width/4, 24)
	if m.width < leftW+20 {
		leftW = m.width / 2
	}

	rightW := m.width - leftW
	bodyH := max(m.height-1, 0)
	domainsH := min(max(bodyH*2/3, 3), bodyH)
	accountsH := bodyH - domainsH

	left := lipgloss.JoinVertical(lipgloss.Left,
		box("Domains", m.domainLines(leftW-2, domainsH-2), leftW, domainsH, m.focus == paneDomains),
		box("Accounts", m.accountLines(leftW-2, accountsH-2), leftW, accountsH, m.focus == paneAccounts),
	)
	right := box("Records", m.recordLines(rightW-2, bodyH-2), rightW, bodyH, m.focus == paneRecords)

	view := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, left, right),
		m.statusBar(),
	)

	switch {
	case m.help:
		view = overlay(view, m.helpView(), m.width, m.height)
	case m.unseal != nil:
		view = overlay(view, m.unsealView(), m.width, m.height)
	case m.addForm != nil:
		view = overlay(view, m.addAccountView(), m.width, m.height)
	case m.keychain != nil:
		view = overlay(view, m.keychainView(), m.width, m.height)
	case m.remove != nil:
		view = overlay(view, m.removeAccountView(), m.width, m.height)
	}

	// Terminals too small to draw the panes in get a clipped view.
	return clip(view, m.width, m.height)
}

// clip cuts s to at most w columns and h lines.
func clip(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	lines = lines[:min(len(lines), max(h, 0))]

	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "")
	}

	return strings.Join(lines, "\n")
}

func (m *Model) domainLines(w, h int) []string {
	lines := m.searchLines(paneDomains, w)
	h -= len(lines)

	start, end := window(len(m.visible), m.cursor[paneDomains], h)

	for i := start; i < end; i++ {
		// Stale Zones are dimmed. The markers show the Live Zone, then the
		// Refresh, else the Serial Check hint.
		z := m.visible[i]
		lines = append(lines, m.row(paneDomains, i, " "+m.zoneLabel(z), w-3, m.zoneStyle(z.ID))+" "+m.liveMarker(z)+m.zoneMarker(z.ID))
	}

	if len(m.visible) == 0 && len(m.zones) > 0 {
		lines = append(lines, hintStyle.Render(" No matching Zones."))
	}

	return lines
}

// zoneStyle dims a Stale Zone and its Records; a Validated Zone is drawn plain.
func (m *Model) zoneStyle(id int64) lipgloss.Style {
	if m.validatedZone(id) {
		return lipgloss.NewStyle()
	}

	return staleStyle
}

// zoneMarker is the one-column marker drawn after a Zone: its Refresh
// state once it has one, else its Serial Check hint, which never
// contradicts a Refresh.
func (m *Model) zoneMarker(id int64) string {
	if marker := m.refreshOf(id).marker(); marker != "" {
		return marker
	}

	return m.freshnessOf(id).marker()
}

// zoneLabel names a Zone in the Domains pane. A zone name that exists in
// several Accounts carries its Account, so the copies can be told apart.
func (m *Model) zoneLabel(z dns.Zone) string {
	if !m.shared[nameKey(z.Name)] {
		return z.Name
	}

	if a, ok := m.account(z.AccountID); ok {
		return z.Name + " · " + a.Label()
	}

	return z.Name
}

func (m *Model) accountLines(w, h int) []string {
	start, end := window(len(m.accounts), m.cursor[paneAccounts], h)

	var lines []string
	for i := start; i < end; i++ {
		text := " " + m.accounts[i].Label()
		if m.accounts[i].ID == m.accountFilter {
			text += " ✓" // the Domains pane shows only this Account's Zones
		}

		lines = append(lines, m.row(paneAccounts, i, text, w, lipgloss.NewStyle()))
	}

	return lines
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
			fmt.Sprintf("  No Accounts yet: press %s to add your first Account.", m.keys.keysFor(ActionAddAccount)),
			"",
			"  An Account is a Provider, a name and an API token.",
			hintStyle.Render("  dnscli keeps Accounts in its local database; it is not a disposable cache."),
		}
	case m.open == nil:
		return []string{"", fmt.Sprintf("  Press %s on a Zone to see its cached Records.", m.keys.keysFor(ActionSelect))}
	case m.recordsLoading:
		return []string{"", hintStyle.Render("  loading…")}
	case m.recordsErr != nil:
		return []string{"", errorStyle.Render("  Could not read Records for " + m.open.Name + ".")}
	case len(m.records) == 0:
		return []string{"", "  No cached Records for " + m.open.Name + "."}
	}

	cols := recordColumns(w)
	header := headerStyle.Render(cols.format("Name", "Type", "Content", "TTL"))

	lines := append(m.searchLines(paneRecords, w), header)

	records := m.visibleRecords
	start, end := window(len(records), m.cursor[paneRecords], h-len(lines))

	for i := start; i < end; i++ {
		r := records[i]
		lines = append(lines, m.row(paneRecords, i, cols.format(r.Name, r.Type, r.Value, strconv.FormatUint(uint64(r.TTL), 10)), w, m.zoneStyle(m.open.ID)))
	}

	if len(records) == 0 {
		lines = append(lines, hintStyle.Render("  No matching Records."))
	}

	return lines
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

	return style.Render(fit(text, w))
}

// fit truncates s to w columns, marking the cut with …, and pads it with
// spaces to exactly w columns.
func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "…")

	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
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
	return " " + fit(name, c.name) + " " + fit(typ, c.typ) + " " + fit(content, c.content) + " " + fit(ttl, c.ttl)
}

// window returns the range [start, end) of n list items that fits in h rows
// and keeps cursor visible, so only the visible rows are rendered.
func window(n, cursor, h int) (start, end int) {
	h = max(h, 1) // a box always has at least one row
	if n <= h {
		return 0, n
	}

	start = min(max(cursor-h+1, 0), n-h)

	return start, start + h
}

// statusBar renders the status line with the session state at its right
// edge. A terminal too narrow for both keeps the status line.
func (m *Model) statusBar() string {
	tag := sealedStyle.Render("sealed · read-only ")
	if !m.sealed() {
		tag = statusStyle.Render("unsealed ")
	}

	room := m.width - lipgloss.Width(tag) - 1
	if room < 20 {
		return ansi.Truncate(m.statusLine(), m.width, "…")
	}

	left := ansi.Truncate(m.statusLine(), room, "…")

	return left + strings.Repeat(" ", m.width-lipgloss.Width(left)-lipgloss.Width(tag)) + tag
}

func (m *Model) statusLine() string {
	switch {
	case m.err != nil:
		return errorStyle.Render(" " + m.err.Error())
	case m.recordsErr != nil:
		return errorStyle.Render(" " + m.recordsErr.Error())
	case m.profileErr != nil:
		return errorStyle.Render(" " + m.profileErr.Error())
	case m.notice.err:
		return errorStyle.Render(" " + m.notice.text)
	case m.notice.text != "":
		return statusStyle.Render(" " + m.notice.text)
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

	// A Refresh outranks the Serial Check hint, which is never shown next to it.
	if refresh := m.refreshOf(z.ID).describe(m.keys.keysFor(ActionRefresh)); refresh != "" {
		parts = append(parts, refresh)
	} else if hint := m.freshnessOf(z.ID).describe(z.Serial); hint != "" {
		parts = append(parts, hint)
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

		sb.WriteString("\n" + style.Render(b.Left) + fit(line, innerW) + style.Render(b.Right))
	}

	sb.WriteString("\n" + style.Render(b.BottomLeft+strings.Repeat(b.Bottom, innerW)+b.BottomRight))

	return sb.String()
}
