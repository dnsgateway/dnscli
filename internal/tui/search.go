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

// startSearch opens the search input in the focused pane, if it has one and
// there is something to search.
func (m *Model) startSearch() {
	if m.searchable(m.focus) == 0 {
		return
	}

	m.search[m.focus].typing = true
}

// searchable returns how many items a pane's search looks through: all of
// its items before any filter. Panes without a search have none.
func (m *Model) searchable(p pane) int {
	switch p {
	case paneDomains:
		return len(m.zones)
	case paneRecords:
		return len(m.records)
	}

	return 0
}

// clearSearch clears the focused pane's search filter and leaves search. It
// does nothing when the pane has no search, so the cursor stays put.
func (m *Model) clearSearch() {
	if m.search[m.focus] == (search{}) {
		return
	}

	m.search[m.focus] = search{}
	m.cursor[m.focus] = 0
	m.syncDomainsQuery()
}

// syncDomainsQuery hands the Domains search query to the Zone filter, so it
// narrows the Domains pane together with the Account filter.
func (m *Model) syncDomainsQuery() {
	if m.focus != paneDomains {
		return
	}

	f := m.filter
	f.query = m.search[paneDomains].query
	m.setFilter(f)
}

// typeIntoSearch sends a typed key to the focused pane's search input, and
// reports whether it did. Keys that are not text still reach actions, and so
// do Alt+key and the keys that leave the search: Close, Select, NextPane and
// PrevPane act even when bound to a printable key.
func (m *Model) typeIntoSearch(msg tea.KeyMsg) bool {
	s := &m.search[m.focus]
	if m.help || !s.typing || msg.Alt {
		return false
	}

	switch m.keys.action(msg.String()) {
	case ActionClose, ActionSelect, ActionNextPane, ActionPrevPane:
		return false
	}

	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		s.query += string(msg.Runes)
	case tea.KeyBackspace, tea.KeyCtrlH: // some terminals send ^H for Backspace
		r := []rune(s.query)
		s.query = string(r[:max(len(r)-1, 0)])
	default:
		return false
	}

	m.cursor[m.focus] = 0
	m.syncDomainsQuery()

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

// visibleRecords are the open Zone's Records the Records pane lists, after
// its search filter, which matches a Record's name, type or value.
func (m *Model) visibleRecords() []dns.Record {
	q := strings.ToLower(m.search[paneRecords].query)
	if q == "" {
		return m.records
	}

	var out []dns.Record

	for _, r := range m.records {
		for _, field := range []string{r.Name, r.Type, r.Value} {
			if strings.Contains(strings.ToLower(field), q) {
				out = append(out, r)

				break
			}
		}
	}

	return out
}
