package tui

import (
	"errors"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// lookup is how far this session has got with a zone name's Delegation
// lookup or its DS Check, or with a Zone's key lookup.
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
	// at is when the resolver answered.
	at time.Time
}

// delegationTTL is how long a resolved Delegation is trusted before a
// reload resolves it again.
const delegationTTL = 10 * time.Minute

// delegationMsg carries the result of one Delegation lookup.
type delegationMsg struct {
	name string // nameKey of the zone name
	ns   []string
	err  error
}

// queueLookups runs after each cache (re)load. It queues a Delegation
// lookup for every zone name this session has not resolved yet, once per
// zone name however many Accounts have it, and again for every name whose
// lookup failed or whose result is older than delegationTTL: once per
// reload, like the Serial Checks. It forgets the Delegations and DS Checks
// of the names that left the cache, and the key lookups of the Zones that
// did.
func (m *Model) queueLookups() tea.Cmd {
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
			m.lookups.queue = append(m.lookups.queue, lookupJob{kind: lookupDelegation, name: k})
		case d.state == lookupFailed,
			d.state == lookupDone && m.deps.now().Sub(d.at) >= delegationTTL:
			*d = delegation{}
			m.lookups.queue = append(m.lookups.queue, lookupJob{kind: lookupDelegation, name: k})
		}
	}

	for k := range m.delegations {
		if !names[k] {
			delete(m.delegations, k)
			m.lookups.drop(lookupJob{kind: lookupDelegation, name: k})
		}
	}

	for k := range m.parentDS {
		if !names[k] {
			delete(m.parentDS, k)
			m.lookups.drop(lookupJob{kind: lookupDS, name: k})
		}
	}

	for id := range m.signingKeys {
		if _, ok := m.zoneByID[id]; !ok {
			delete(m.signingKeys, id)
			m.lookups.drop(lookupJob{kind: lookupKeys, zoneID: id})
		}
	}

	return m.startLookups()
}

// forgetDelegations forgets what this session resolved for an Account's
// zone names, so the next reload resolves them again, such as after the
// Account is re-listed.
func (m *Model) forgetDelegations(accountID int64) {
	for _, z := range m.zones {
		if z.AccountID != accountID {
			continue
		}

		k := nameKey(z.Name)
		if d, ok := m.delegations[k]; ok && (d.state == lookupDone || d.state == lookupFailed) {
			delete(m.delegations, k)
		}
	}
}

// lookupKind is what a lookupJob looks up.
type lookupKind int

const (
	lookupDelegation lookupKind = iota // the zone name's Delegation
	lookupDS                           // the DS its parent serves: a DS Check
	lookupKeys                         // a Zone's key-signing keys
)

// lookupJob is one lookup the lookups queue schedules: for a zone name, by
// nameKey, or, for a key lookup, for a Zone, by ID.
type lookupJob struct {
	kind   lookupKind
	name   string
	zoneID int64
}

// startLookups starts queued Delegation lookups, DS Checks and key
// lookups, which share one limit, up to it.
func (m *Model) startLookups() tea.Cmd {
	var cmds []tea.Cmd

	for m.lookups.running < m.deps.serialChecks() && len(m.lookups.queue) > 0 {
		job := m.lookups.queue[0]
		m.lookups.queue = m.lookups.queue[1:]

		cmd := m.startLookup(job)
		if cmd == nil {
			continue // left the cache meanwhile
		}

		m.lookups.running++

		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}

// startLookup marks a job's zone name as resolving and returns the command
// that asks the resolver; nil when the name is no longer tracked.
func (m *Model) startLookup(job lookupJob) tea.Cmd {
	ctx, resolver, k := m.ctx, m.deps.Resolver, job.name

	switch job.kind {
	case lookupDS:
		c, ok := m.parentDS[k]
		if !ok {
			return nil
		}

		c.state = lookupResolving

		return func() tea.Msg {
			ds, _, err := resolver.DSAt(ctx, k)

			return dsCheckedMsg{name: k, ds: ds, err: err}
		}
	case lookupKeys:
		c, ok := m.signingKeys[job.zoneID]
		if !ok {
			return nil
		}

		c.state = lookupResolving
		id, zone, servers := job.zoneID, c.zone, c.servers

		return func() tea.Msg {
			keys, err := resolver.DNSKEYAt(ctx, zone, servers)

			return keysCheckedMsg{zoneID: id, keys: keys, err: err}
		}
	default:
		d, ok := m.delegations[k]
		if !ok {
			return nil
		}

		d.state = lookupResolving

		return func() tea.Msg {
			ns, err := resolver.NS(ctx, k)

			return delegationMsg{name: k, ns: ns, err: err}
		}
	}
}

// lookupAnswered frees the slot of a lookup that answered, whatever its
// kind, and starts the next one.
func (m *Model) lookupAnswered() tea.Cmd {
	m.lookups.running--

	return m.startLookups()
}

// recordDelegation records what the resolver answered for a zone name,
// by nameKey, whoever asked it: a lookup, or a check just before a Zone
// write such as an Offboarding. A name this session does not track, as
// it left the cache, is not recorded. A failure leaves the Live Zone
// unknown and raises no error: it shows only in the Edit Zone dialog.
func (m *Model) recordDelegation(name string, ns []string, err error) {
	d, ok := m.delegations[name]
	if !ok {
		return
	}

	// A lookup queued or in flight still runs; what it answers then
	// replaces this.
	switch {
	case err != nil:
		*d = delegation{state: lookupFailed, err: err}
	case len(ns) == 0:
		*d = delegation{state: lookupFailed, err: errNoNameservers}
	default:
		*d = delegation{state: lookupDone, ns: nameSet(ns), at: m.deps.now()}
	}
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
// nameservers are exactly the delegated ones, compared as sets; the cache
// load made them a nameSet. It is unknown while the Delegation is not
// resolved, or when the Zone's expected nameservers are not cached.
func (m *Model) liveness(z dns.Zone) liveness {
	d, ok := m.delegations[nameKey(z.Name)]
	if !ok || d.state != lookupDone {
		return liveUnknown
	}

	return livenessOf(z.Nameservers, d.ns)
}

// livenessOf is the Live Zone rule: a Zone whose expected nameservers ns
// are exactly the delegated ones, a nameSet, compared as sets, is the
// Live Zone; it is unknown without ns.
func livenessOf(ns, delegated []string) liveness {
	switch {
	case len(ns) == 0:
		return liveUnknown
	case slices.Equal(nameSet(ns), delegated):
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

// liveZoneText says, in a few words, whether z is the Live Zone, for
// anywhere that shows one Zone's state, such as the Zone Status bar.
func (m *Model) liveZoneText(z dns.Zone) string {
	switch m.liveness(z) {
	case liveYes:
		return "Live Zone"
	case liveNo:
		return "not the Live Zone"
	}

	if m.checkingLive(z) {
		return "checking the Live Zone…"
	}

	return "Live Zone unknown"
}

// checkingLive reports whether whether z is the Live Zone is being found
// out: its Delegation is being resolved, and its nameservers are cached.
func (m *Model) checkingLive(z dns.Zone) bool {
	d, ok := m.delegations[nameKey(z.Name)]

	return ok && len(z.Nameservers) > 0 && (d.state == lookupQueued || d.state == lookupResolving)
}
