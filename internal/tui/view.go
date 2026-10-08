package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dnsgateway/dnsprovider"

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
	addedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	modifiedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	deletedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Strikethrough(true)
	// cursorStyle marks the highlighted item of a popup, such as a picker.
	cursorStyle = lipgloss.NewStyle().Reverse(true)
	// staleStyle dims cached data that has not been confirmed in this session.
	staleStyle = lipgloss.NewStyle().Faint(true)
)

// layout is the size of each pane, borders included.
type layout struct {
	leftW, rightW, bodyH, domainsH, accountsH int
}

// layout sizes the panes to the terminal. Panes keep their minimum sizes
// while the terminal has room for them, and shrink to fit it when it does not.
func (m *Model) layout() layout {
	leftW := max(m.width/4, 24)
	if m.width < leftW+20 {
		leftW = m.width / 2
	}

	bodyH := max(m.height-2, 0) // the Zone Status bar and the status line take a row each
	domainsH := min(max(bodyH*2/3, 3), bodyH)

	return layout{leftW: leftW, rightW: m.width - leftW, bodyH: bodyH, domainsH: domainsH, accountsH: bodyH - domainsH}
}

// listRows is how many list items a pane shows at once: its height less
// its borders, its search input and, in the Records pane, the header.
func (m *Model) listRows(p pane) int {
	l := m.layout()

	switch p {
	case paneDomains:
		return max(l.domainsH-2-len(m.searchLines(paneDomains, l.leftW-2)), 1)
	case paneAccounts:
		return max(l.accountsH-2, 1)
	case paneRecords:
		return max(l.bodyH-2-len(m.searchLines(paneRecords, l.rightW-2))-1, 1)
	}

	return 1
}

// View renders the Zone Status bar, the three panes and the status line.
func (m *Model) View() string {
	l := m.layout()
	leftW, rightW, bodyH, domainsH, accountsH := l.leftW, l.rightW, l.bodyH, l.domainsH, l.accountsH

	left := lipgloss.JoinVertical(lipgloss.Left,
		box(paneNames[paneDomains], m.domainLines(leftW-2, domainsH-2), leftW, domainsH, m.focus == paneDomains),
		box(paneNames[paneAccounts], m.accountLines(leftW-2, accountsH-2), leftW, accountsH, m.focus == paneAccounts),
	)
	right := box(paneNames[paneRecords], m.recordLines(rightW-2, bodyH-2), rightW, bodyH, m.focus == paneRecords)

	view := lipgloss.JoinVertical(lipgloss.Left,
		m.zoneStatusRow(),
		lipgloss.JoinHorizontal(lipgloss.Top, left, right),
		m.statusLineRow(),
	)

	switch {
	case m.help:
		view = overlay(view, m.helpView(), m.width, m.height)
	case m.unseal != nil:
		view = overlay(view, m.unsealView(), m.width, m.height)
	case m.addForm != nil:
		view = overlay(view, m.addAccountView(), m.width, m.height)
		if m.addForm.picker != nil {
			view = overlay(view, m.addForm.picker.view(m.height), m.width, m.height)
		}
	case m.keychain != nil:
		view = overlay(view, m.keychainView(), m.width, m.height)
	case m.remove != nil:
		view = overlay(view, m.removeAccountView(), m.width, m.height)
	case m.zoneSettings != nil:
		view = overlay(view, m.zoneSettingsView(), m.width, m.height)
	case m.onboard != nil:
		view = overlay(view, m.onboardView(), m.width, m.height)
	case m.recordForm != nil:
		view = overlay(view, m.recordFormView(), m.width, m.height)
		if m.recordForm.picker != nil {
			view = overlay(view, m.recordForm.picker.view(m.height), m.width, m.height)
		}
	case m.templateForm != nil:
		view = overlay(view, m.templateFormView(), m.width, m.height)
		if m.templateForm.picker != nil {
			view = overlay(view, m.templateForm.picker.view(m.height), m.width, m.height)
		}
	case m.preview != nil:
		view = overlay(view, m.previewView(), m.width, m.height)
	case m.delegationOf != "":
		view = overlay(view, m.delegationView(), m.width, m.height)
	case m.tenantPick != nil:
		view = overlay(view, m.tenantPick.picker.view(m.height), m.width, m.height)
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
		lines = append(lines, m.row(paneDomains, i, " "+markedLabel(m.zoneLabel(z), hostingMark(z.State), w-4), w-3, m.zoneStyle(z.ID))+
			" "+m.liveMarker(z)+m.zoneMarker(z))
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
// contradicts a Refresh. A Zone that is not the Live Zone has no hint.
func (m *Model) zoneMarker(z dns.Zone) string {
	if marker := m.refreshOf(z.ID).marker(); marker != "" {
		return marker
	}

	if !m.serialCheckApplies(z) {
		return " "
	}

	// The cached serial is behind the nameservers' until the poll stores
	// the new one: not a sign of a stale Zone.
	if m.serialUpdating(z.ID) {
		return checkingStyle.Render("…")
	}

	return m.freshnessOf(z.ID).marker()
}

// noSerialCheck says why a Zone that is not the Live Zone shows no Serial
// Check hint.
const noSerialCheck = "no Serial Check: the resolver answers for the Live Zone"

// serialCheckApplies reports whether a Zone's Serial Check says anything
// about it. The resolver answers for the Live Zone, so the serial it
// gives is not that of a Zone known not to be the Live Zone.
func (m *Model) serialCheckApplies(z dns.Zone) bool {
	return m.liveness(z) != liveNo
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

// minMarkedLabel is the fewest columns of label a mark leaves: a label cut
// shorter would say nothing.
const minMarkedLabel = 2

// markedLabel fits a label and the mark that follows it in w columns. The
// label is cut short, never the mark. When w cannot hold the mark and
// minMarkedLabel columns of label, or the whole label when it is shorter,
// the mark is left out and the label alone is cut to w.
func markedLabel(label, mark string, w int) string {
	avail := w - lipgloss.Width(mark)
	if mark == "" || avail < min(lipgloss.Width(label), minMarkedLabel) {
		return ansi.Truncate(label, max(w, 0), "…")
	}

	return ansi.Truncate(label, avail, "…") + mark
}

// hostingMark follows the label of a Zone its Provider is not serving: one
// Onboarded and waiting for Delegation, or one whose Delegation left. An
// Active Zone, or one whose Hosting State is not known yet, has none.
func hostingMark(s dnsprovider.HostingState) string {
	switch s {
	case dnsprovider.HostingPending:
		return " · pending"
	case dnsprovider.HostingMoved:
		return " · moved"
	default:
		return ""
	}
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
			fmt.Sprintf("  No Accounts yet: press %s to add your first Account.", m.keys.keysFor(ActionInsert)),
			"",
			"  An Account is a Provider, a name and its credentials.",
			hintStyle.Render("  dnscli keeps Accounts in its local database; it is not a disposable cache."),
		}
	case m.open == nil:
		return []string{"", fmt.Sprintf("  Press %s on a Zone to see its cached Records.", m.keys.keysFor(ActionSelect))}
	case m.recordsLoading:
		return []string{"", hintStyle.Render("  loading…")}
	case m.recordsErr != nil:
		return []string{"", errorStyle.Render("  Could not read Records for " + m.open.Name + ".")}
	case len(m.rows) == 0:
		return []string{"", "  No cached Records for " + m.open.Name + "."}
	}

	cols := recordColumns(w)
	header := headerStyle.Render(cols.format(" ", "Name", "Type", "Content", "TTL"))

	lines := append(m.searchLines(paneRecords, w), header)

	records := m.visibleRecords
	start, end := window(len(records), m.cursor[paneRecords], h-len(lines))

	for i := start; i < end; i++ {
		r := records[i].rec
		style := m.zoneStyle(m.open.ID)
		if k := records[i].kind; k != 0 {
			style = changeStyle(k)
		}

		lines = append(lines, m.row(paneRecords, i, cols.format(changeMark(records[i].kind), r.Name, r.Type, r.Value, strconv.FormatUint(uint64(r.TTL), 10)), w, style))
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
	rest := max(w-3-c.typ-c.ttl-3, 10)
	c.name = rest * 2 / 5
	c.content = rest - c.name

	return c
}

// format lays out a Records pane row. mark is the one-column mark of a
// pending change.
func (c columns) format(mark, name, typ, content, ttl string) string {
	return " " + mark + " " + fit(name, c.name) + " " + fit(typ, c.typ) + " " + fit(content, c.content) + " " + fit(ttl, c.ttl)
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

// zoneStatusRow is the Zone Status bar at the top of the view: the open
// Zone's Zone Status, with the session state at its right edge.
func (m *Model) zoneStatusRow() string {
	tag := sealedStyle.Render("sealed · read-only ")
	if !m.sealed() {
		tag = statusStyle.Render("unsealed ")
	}

	left := ""
	if m.loaded && m.open != nil {
		left = " " + renderZoneStatus(m.zoneStatusItems(*m.open))
	}

	return spread(left, tag, m.width)
}

// statusLineRow is the status line at the bottom of the view, with the
// key hints at its right edge. A form's keys keep their place and cut the
// status line short; otherwise the status line, which may be an error,
// keeps its place and the help and quit keys give way.
func (m *Model) statusLineRow() string {
	left, right := m.statusLine(), hintStyle.Render(m.keyHints()+" ")

	form := m.recordForm != nil || m.templateForm != nil || m.addForm != nil

	switch {
	case !form && lipgloss.Width(left)+1+lipgloss.Width(right) > m.width:
		return ansi.Truncate(left, m.width, "…")
	case form && m.width-lipgloss.Width(right)-1 < 20:
		right = ansi.TruncateLeft(right, max(lipgloss.Width(right)-m.width, 0), "…")

		return strings.Repeat(" ", m.width-lipgloss.Width(right)) + right
	}

	return spread(left, right, m.width)
}

// keyHints are the keys of the open form, or the keys for help and quit.
func (m *Model) keyHints() string {
	if m.addForm != nil {
		// An edited Account's Provider has no values to change or list.
		return m.formKeysHint(m.addForm.editing == nil)
	}

	if m.recordForm != nil || m.templateForm != nil {
		return m.formKeysHint(true)
	}

	return fmt.Sprintf("%s help · %s quit", m.keys.keysFor(ActionHelp), m.keys.keysFor(ActionQuit))
}

// spread lays out a row w columns wide: left at its start and right at its
// end. When both do not fit, left is cut short; a row too narrow to keep
// 20 columns of left drops right.
func spread(left, right string, w int) string {
	room := w - lipgloss.Width(right) - 1
	if room < 20 {
		return ansi.Truncate(left, w, "…")
	}

	left = ansi.Truncate(left, room, "…")

	return left + strings.Repeat(" ", w-lipgloss.Width(left)-lipgloss.Width(right)) + right
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

	return ""
}

func (m *Model) zoneStatus(z dns.Zone) string {
	parts := []string{z.Name}

	if a, ok := m.account(z.AccountID); ok {
		parts = append(parts, a.Label())
	}

	// A Refresh outranks the Serial Check hint, which is never shown next to it.
	if refresh := m.refreshOf(z.ID).describe(m.keys.keysFor(ActionRefresh)); refresh != "" {
		parts = append(parts, refresh)
	} else if !m.serialCheckApplies(z) {
		parts = append(parts, noSerialCheck)
	} else if hint := m.freshnessOf(z.ID).describe(z.Serial); hint != "" && !m.serialUpdating(z.ID) {
		parts = append(parts, hint)
	}

	if m.serialUpdating(z.ID) {
		parts = append(parts, "serial updating after Apply")
	}

	if cs := m.pendingOf(z.ID); cs != nil {
		parts = append(parts, fmt.Sprintf("%s (%s preview)", plural(len(cs.Changes), "pending change"), m.keys.keysFor(ActionPreviewChanges)))
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

// changeStyle colours a Record with a pending change: added, modified or deleted.
func changeStyle(k dns.ChangeKind) lipgloss.Style {
	switch k {
	case dns.ChangeAdd:
		return addedStyle
	case dns.ChangeModify:
		return modifiedStyle
	case dns.ChangeDelete:
		return deletedStyle
	}

	return lipgloss.NewStyle()
}
