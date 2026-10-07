package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// search is one pane's search filter.
type search struct {
	typing bool // the search input has the keyboard
	query  string
}

// startSearch opens the search input in the focused pane, if it has one.
func (m *Model) startSearch() {
	if m.focus != paneDomains {
		return
	}

	m.search[m.focus].typing = true
}

// typeIntoSearch sends a typed key to the focused pane's search input, and
// reports whether it did. Keys that are not text still reach actions.
func (m *Model) typeIntoSearch(msg tea.KeyMsg) bool {
	s := &m.search[m.focus]
	if m.help || !s.typing {
		return false
	}

	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		s.query += string(msg.Runes)
	default:
		return false
	}

	m.cursor[m.focus] = 0

	return true
}

// searchLines renders a pane's search input: none while the pane has no
// search, and a "/query" line, with a cursor while typing, when it does.
func (m *Model) searchLines(p pane, w int) []string {
	s := m.search[p]
	if !s.typing && s.query == "" {
		return nil
	}

	if !s.typing {
		return []string{hintStyle.Render(fit(" /"+s.query, w))}
	}

	return []string{fit(" /"+s.query+"▏", w)}
}

// visibleZones are the Zones the Domains pane lists, after its search filter.
func (m *Model) visibleZones() []dns.Zone {
	q := strings.ToLower(m.search[paneDomains].query)
	if q == "" {
		return m.zones
	}

	var out []dns.Zone

	for _, z := range m.zones {
		if strings.Contains(strings.ToLower(z.Name), q) {
			out = append(out, z)
		}
	}

	return out
}
