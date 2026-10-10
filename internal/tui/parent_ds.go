package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// dsCheck is what this session found of the Delegation Signer (DS)
// records a zone name's parent zone serves. It is held in memory only,
// like the Delegation: a zone name with none was not checked this session.
type dsCheck struct {
	state lookup
	// ds are the DS records the parent serves, in presentation form.
	ds []string
	// ttl is the TTL of their set.
	ttl time.Duration
	err error
	// at is when the parent answered.
	at time.Time
}

// dsCheckedMsg carries the result of one DS check.
type dsCheckedMsg struct {
	name string // nameKey of the zone name
	ds   []string
	ttl  time.Duration
	err  error
}

// checkParentDS queues a check of the DS a zone name's parent serves,
// such as when its Zone is opened or Refreshed, unless one is queued or
// in flight. It runs under the limit the Delegation lookups share.
func (m *Model) checkParentDS(z dns.Zone) tea.Cmd {
	if m.deps.Resolver == nil {
		return nil
	}

	if m.parentDS == nil {
		m.parentDS = map[string]*dsCheck{}
	}

	k := nameKey(z.Name)

	c, ok := m.parentDS[k]

	switch {
	case !ok:
		c = &dsCheck{}
		m.parentDS[k] = c
	case c.state == lookupQueued || c.state == lookupResolving:
		return nil
	}

	c.state = lookupQueued
	m.lookups.queue = append(m.lookups.queue, lookupJob{name: k, ds: true})

	return m.startLookups()
}

// startDSCheck starts the DS check of a zone name; nil when the name is
// no longer tracked.
func (m *Model) startDSCheck(k string) tea.Cmd {
	c, ok := m.parentDS[k]
	if !ok {
		return nil
	}

	c.state = lookupResolving

	ctx, resolver := m.ctx, m.deps.Resolver

	return func() tea.Msg {
		ds, ttl, err := resolver.DSAt(ctx, k)

		return dsCheckedMsg{name: k, ds: ds, ttl: ttl, err: err}
	}
}

// dsChecked records a DS check result and starts the next lookup.
func (m *Model) dsChecked(msg dsCheckedMsg) tea.Cmd {
	m.lookups.running--

	if c, ok := m.parentDS[msg.name]; ok {
		if msg.err != nil {
			*c = dsCheck{state: lookupFailed, err: msg.err}
		} else {
			*c = dsCheck{state: lookupDone, ds: msg.ds, ttl: msg.ttl, at: m.deps.now()}
		}
	}

	return m.startLookups()
}

// dsPresence is whether a zone name's parent serves a DS, as far as this
// session knows.
type dsPresence int

const (
	// dsUnknown is a parent not checked this session, being checked, or
	// whose check failed: never taken as no DS.
	dsUnknown dsPresence = iota
	dsNone
	dsServed
)

// dsPresenceOf says whether a zone name's parent serves a DS.
func (m *Model) dsPresenceOf(zone string) dsPresence {
	c, ok := m.parentDS[nameKey(zone)]

	switch {
	case !ok || c.state != lookupDone:
		return dsUnknown
	case len(c.ds) == 0:
		return dsNone
	}

	return dsServed
}

// parentDSText says, in a few words, what this session found of the DS
// records z's parent serves, for the Zone Status bar.
func (m *Model) parentDSText(z dns.Zone) string {
	c, ok := m.parentDS[nameKey(z.Name)]
	if !ok {
		return "DS at parent: not checked this session"
	}

	switch c.state {
	case lookupQueued, lookupResolving:
		return "DS at parent: checking…"
	case lookupFailed:
		return "DS at parent: unknown"
	}

	if len(c.ds) == 0 {
		return "DS at parent: no"
	}

	return fmt.Sprintf("DS at parent: yes (%d)", len(c.ds))
}
