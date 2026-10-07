package tui

import (
	"errors"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// lookup is how far this session has resolved a zone name's Delegation.
type lookup int

const (
	lookupQueued    lookup = iota // waiting for a free slot
	lookupResolving               // the resolver is being asked
	lookupDone                    // the resolver answered
	lookupFailed                  // the resolver failed
)

// delegation is what this session knows about one zone name's Delegation.
// It is held in memory only, like freshness.
type delegation struct {
	state lookup
	// ns are the delegated nameservers, as nameKey keys, sorted.
	ns  []string
	err error
}

// delegationMsg carries the result of one Delegation lookup.
type delegationMsg struct {
	name string // nameKey of the zone name
	ns   []string
	err  error
}

// queueDelegations runs after each cache (re)load. It queues a Delegation
// lookup for every zone name this session has not resolved yet, once per
// zone name however many Accounts have it, and again for every name whose
// lookup failed: once per reload, like the Serial Checks. It forgets the
// names that left the cache.
func (m *Model) queueDelegations() tea.Cmd {
	if m.deps.Resolver == nil {
		return nil
	}

	if m.delegations == nil {
		m.delegations = map[string]*delegation{}
	}

	names := map[string]bool{}

	for _, z := range m.zones {
		k := nameKey(z.Name)
		if names[k] {
			continue
		}

		names[k] = true

		d, ok := m.delegations[k]

		switch {
		case !ok:
			m.delegations[k] = &delegation{}
			m.lookups.queue = append(m.lookups.queue, k)
		case d.state == lookupFailed:
			*d = delegation{}
			m.lookups.queue = append(m.lookups.queue, k)
		}
	}

	for k := range m.delegations {
		if !names[k] {
			delete(m.delegations, k)
			m.lookups.drop(k)
		}
	}

	return m.startDelegations()
}

// startDelegations starts queued Delegation lookups up to the limit.
func (m *Model) startDelegations() tea.Cmd {
	var cmds []tea.Cmd

	for m.lookups.running < m.deps.serialChecks() && len(m.lookups.queue) > 0 {
		k := m.lookups.queue[0]
		m.lookups.queue = m.lookups.queue[1:]

		d, ok := m.delegations[k]
		if !ok {
			continue // left the cache meanwhile
		}

		d.state = lookupResolving
		m.lookups.running++

		ctx, resolver := m.ctx, m.deps.Resolver

		cmds = append(cmds, func() tea.Msg {
			ns, err := resolver.NS(ctx, k)

			return delegationMsg{name: k, ns: ns, err: err}
		})
	}

	return tea.Batch(cmds...)
}

// delegationResolved records a lookup result and starts the next one. A
// failure leaves the Live Zone unknown and raises no error: it shows only
// in the Delegation view.
func (m *Model) delegationResolved(msg delegationMsg) tea.Cmd {
	m.lookups.running--

	if d, ok := m.delegations[msg.name]; ok {
		switch {
		case msg.err != nil:
			*d = delegation{state: lookupFailed, err: msg.err}
		case len(msg.ns) == 0:
			*d = delegation{state: lookupFailed, err: errNoNameservers}
		default:
			*d = delegation{state: lookupDone, ns: nameSet(msg.ns)}
		}
	}

	return m.startDelegations()
}

// nameSet normalises DNS names with nameKey, sorted and without
// duplicates, so two sets compare with slices.Equal.
func nameSet(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, nameKey(n))
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// liveness is whether a Zone is its zone name's Live Zone.
type liveness int

const (
	liveUnknown liveness = iota
	liveYes
	liveNo
)

// liveness says whether z is the Live Zone: whether its expected
// nameservers are exactly the delegated ones, compared as sets. It is
// unknown while the Delegation is not resolved, or when the Zone's
// expected nameservers are not cached.
func (m *Model) liveness(z dns.Zone) liveness {
	d, ok := m.delegations[nameKey(z.Name)]
	if !ok || d.state != lookupDone || len(z.Nameservers) == 0 {
		return liveUnknown
	}

	if slices.Equal(nameSet(z.Nameservers), d.ns) {
		return liveYes
	}

	return liveNo
}

var liveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))

// liveMarker is the one-column marker drawn after the Live Zone in the
// Domains pane.
func (m *Model) liveMarker(z dns.Zone) string {
	if m.liveness(z) == liveYes {
		return liveStyle.Render("●")
	}

	return " "
}

var errNoNameservers = errors.New("the resolver gave no nameservers")
