package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dnsgateway/dnsprovider"
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
		keys := m.keys.keysFor(b.Action)
		if same, ok := sameAs[m.focus][b.Action]; ok {
			keys = strings.Trim(keys+"/"+m.keys.keysFor(same), "/")
		}

		if keys != "" {
			local = append(local, row{keys, b.Help})
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

	// The icon legend shows only when the overlay, borders included, has
	// room for it: the keys come first.
	var legend []string
	if m.focus == paneDomains {
		legend = m.iconLegend(keyW)

		rows := len(lines) + len(legend) + 5 // a blank row and the rule above it, a blank row below, and the borders
		if len(pane) > 0 {
			rows += len(pane) + 2
		}

		if rows > m.height {
			legend = nil
		}
	}

	for _, l := range legend {
		width = max(width, lipgloss.Width(l))
	}

	if len(pane) > 0 {
		lines = append(lines, "", helpRule(paneNames[m.focus], width))
		lines = append(lines, pane...)
	}

	if len(legend) > 0 {
		lines = append(lines, "", helpRule("Domains icons", width))
		lines = append(lines, legend...)
	}

	lines = append(lines, "")

	return box("Help", lines, max(width+2, 24), len(lines)+2, true)
}

// helpRule is the help overlay's rule above a section, titled.
func helpRule(title string, width int) string {
	title = " " + title + " "

	return hintStyle.Render(" ──" + title + strings.Repeat("─", max(width-4-lipgloss.Width(title), 0)) + " ")
}

// iconLegend says what the marks after a Zone in the Domains pane mean,
// drawn as the pane draws them, in two columns, with the marks in a column
// keyW wide like the keys'.
func (m *Model) iconLegend(keyW int) []string {
	icons := []struct{ icon, help string }{
		{modifiedStyle.Render("±"), "pending changes"},
		{liveStyle.Render("●"), "the Live Zone"},
		{checkingStyle.Render("↻"), "refreshing"},
		{hintCurrentStyle.Render("✓"), "validated: may be edited"},
		{hintStaleStyle.Render("⌀"), "registration only"},
		{errorStyle.Render("✗"), "the Refresh failed"},
		{checkingStyle.Render("…"), "checking the serial"},
		{hintCurrentStyle.Render("≈"), "serial matches (a hint)"},
		{hintStaleStyle.Render("!"), "serial differs: maybe stale"},
		{errorStyle.Render("?"), "the serial check failed"},
		{"· pending", "awaiting its Delegation"},
		{"· moved", "Delegation left: not served"},
		{staleStyle.Render("dimmed"), "a Stale Zone, not validated"},
	}

	cells := make([]string, len(icons))
	for i, ic := range icons {
		cells[i] = "  " + ic.icon + strings.Repeat(" ", max(keyW-lipgloss.Width(ic.icon), 0)) + "   " + ic.help
	}

	half := (len(cells) + 1) / 2

	leftW := 0
	for _, c := range cells[:half] {
		leftW = max(leftW, lipgloss.Width(c))
	}

	lines := make([]string, 0, half)
	for i, c := range cells[:half] {
		line := c
		if i+half < len(cells) {
			line += strings.Repeat(" ", leftW-lipgloss.Width(c)) + cells[i+half]
		}

		lines = append(lines, line+"  ")
	}

	return lines
}

// paneBindings are the focused pane's Actions for the help overlay.
// Without Accounts, insert adds one from any pane. The Domains pane's Zone
// writes are offered only in an Unsealed session, for an Account whose
// Provider type can make them (see zoneWriteOffered).
func (m *Model) paneBindings() []paneBinding {
	var out []paneBinding

	if len(m.accounts) == 0 {
		out = append(out, paneBinding{ActionInsert, "add an Account"})
	}

	for _, b := range paneHelp[m.focus] {
		switch {
		case len(m.accounts) == 0 && b.Action == ActionInsert:
		case m.focus == paneDomains && !m.zoneWriteOffered(b.Action):
		default:
			out = append(out, b)
		}
	}

	return out
}

// zoneWriteOffered reports whether the Domains pane offers Action a: any
// Action but its Zone writes, Onboarding (insert) and Offboarding
// (delete), which it offers only in an Unsealed session, Onboarding when
// an Account's Provider type can, Offboarding when the highlighted Zone's
// can, and Offboarding only for a Zone
// known not to be the Live Zone. The keys still refuse otherwise. The
// Edit Zone dialog is offered for every Zone: it shows the Delegation in
// any session, its settings read-only when they cannot be changed.
func (m *Model) zoneWriteOffered(a Action) bool {
	switch a {
	case ActionInsert:
		accounts, _ := m.onboardAccounts()

		return m.key != nil && len(accounts) > 0
	case ActionDelete:
		if m.key == nil || len(m.visible) == 0 {
			return false
		}

		z := m.visible[m.cursor[paneDomains]]
		acc, ok := m.account(z.AccountID)

		return ok && m.accountCan(acc, dnsprovider.CapZoneDeleter) && m.offboardRefusal(z, acc) == nil
	}

	return true
}

// overlay draws fg centered on top of bg.
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
