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
// Action but its Zone writes, Onboarding (insert), Offboarding (delete)
// and the Zone settings, which it offers only in an Unsealed session,
// Onboarding for the Account it would Onboard in, Offboarding and the
// Zone settings for the highlighted Zone's, when that Account's Provider
// type can, and Offboarding only for a Zone known not to be the Live
// Zone. The keys still refuse otherwise.
func (m *Model) zoneWriteOffered(a Action) bool {
	switch a {
	case ActionInsert:
		acc, ok := m.onboardAccount()

		return m.key != nil && ok && m.accountCan(acc, dnsprovider.CapZoneCreator)
	case ActionZoneSettings:
		if m.key == nil || len(m.visible) == 0 {
			return false
		}

		acc, ok := m.account(m.visible[m.cursor[paneDomains]].AccountID)

		return ok && m.accountCan(acc, dnsprovider.CapZoneUpdater)
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
