// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

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
	// ds are the DS records the parent serves, in presentation form,
	// and ttl the TTL of their set.
	ds  []string
	ttl time.Duration
	err error
}

// dsCheckedMsg carries the result of one DS Check.
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
	m.lookups.push(dsJob(k))

	return m.startLookups()
}

// recordDSCheck records what a DS Check found, unless its zone name is no
// longer tracked.
func (m *Model) recordDSCheck(msg dsCheckedMsg) {
	c, ok := m.parentDS[msg.name]
	if !ok {
		return
	}

	if msg.err != nil {
		*c = dsCheck{state: lookupFailed, err: msg.err}
	} else {
		*c = dsCheck{state: lookupDone, ds: msg.ds, ttl: msg.ttl}
	}
}

// dsPresence is whether a zone name's parent serves a DS, as far as this
// session knows.
type dsPresence int

const (
	// dsNotChecked is a parent not checked this session.
	dsNotChecked dsPresence = iota
	// dsChecking is a parent whose check is queued or in flight.
	dsChecking
	// dsUnknown is a parent whose check failed: never taken as no DS.
	dsUnknown
	dsNone
	dsServed
)

// dsPresenceOf says whether a zone name's parent serves a DS.
func (m *Model) dsPresenceOf(zone string) dsPresence {
	c, ok := m.parentDS[nameKey(zone)]
	if !ok {
		return dsNotChecked
	}

	switch c.state {
	case lookupQueued, lookupResolving:
		return dsChecking
	case lookupFailed:
		return dsUnknown
	}

	if len(c.ds) == 0 {
		return dsNone
	}

	return dsServed
}

// dsDoubt says what this session cannot tell of the DS zone's parent
// serves: that it is checking whether it serves one, or could not tell,
// as when it was not checked; empty once it can tell.
func dsDoubt(zone string, ds dsPresence) string {
	switch ds {
	case dsChecking:
		return "dnscli is checking whether the parent zone serves a DS record for " + zone
	case dsNotChecked, dsUnknown:
		return "dnscli could not tell whether the parent zone serves a DS record for " + zone
	}

	return ""
}

// removeDSFirst says to remove the DS zone's parent serves before zone is
// unsigned.
func removeDSFirst(zone string) string {
	return "remove the DS record for " + zone + " at the registrar first"
}

// parentDSText says, in a few words, what this session found of the DS
// records z's parent serves, for the Zone Status bar.
func (m *Model) parentDSText(z dns.Zone) string {
	switch m.dsPresenceOf(z.Name) {
	case dsNotChecked:
		return "DS at parent: not checked this session"
	case dsChecking:
		return "DS at parent: checking…"
	case dsUnknown:
		return "DS at parent: unknown"
	case dsNone:
		return "DS at parent: no"
	}

	return fmt.Sprintf("DS at parent: yes (%d)", len(m.parentDS[nameKey(z.Name)].ds))
}
