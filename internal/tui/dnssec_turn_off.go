package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// confirmZoneEdit asks to confirm the changes the Edit Zone dialog holds,
// unless one of them may not be made now: turning DNSSEC off is refused
// while unsigning would break resolution, and the dialog then says why.
// Asking to turn it off marks the Zone as being turned off this session,
// so the Zone page shows the steps towards it.
func (m *Model) confirmZoneEdit(f *zoneEditForm) {
	f.confirming, f.err = false, ""

	for _, c := range f.changes() {
		if c.setting != dnssecSetting {
			continue
		}

		if c.value != "off" {
			// Turning it on is a Zone write: it passes the write gate
			// (ADR-0001).
			if why := m.writeRefusal(f.zone); why != "" {
				f.err = why

				return
			}

			continue
		}

		m.markTurningOff(f.zone.ID)

		if why := m.unsignRefusal(f.zone); why != "" {
			f.err = why

			return
		}
	}

	f.confirming = true
}

// turnOff is how far turning DNSSEC off for a Zone went this session.
type turnOff int

const (
	turnOffNone turnOff = iota
	// turnOffAsked is the user asking to turn it off.
	turnOffAsked
	// turnOffSent is its Provider turning it off.
	turnOffSent
)

// markTurningOff notes that the user asked to turn DNSSEC off for a Zone,
// unless its Provider already did. It is held for this session only: no
// step is stored.
func (m *Model) markTurningOff(zoneID int64) {
	if m.turningOff == nil {
		m.turningOff = map[int64]turnOff{}
	}

	if m.turningOff[zoneID] == turnOffNone {
		m.turningOff[zoneID] = turnOffAsked
	}
}

// cancelTurningOff ends turning DNSSEC off for the Zone of the Edit Zone
// dialog when the user picks it on again and saves: its Provider has not
// turned it off yet, so nothing is written.
func (m *Model) cancelTurningOff(f *zoneEditForm) {
	if f.dnssecPicked && f.picked(dnssecSetting) == "on" && m.turningOff[f.zone.ID] == turnOffAsked {
		delete(m.turningOff, f.zone.ID)
	}
}

// dnssecTurned notes DNSSEC turned "on" or "off" for a Zone by its
// Provider: off is then waited on until its nameservers stop serving the
// keys; on ends any turning off.
func (m *Model) dnssecTurned(zoneID int64, v string) {
	if v != "off" {
		delete(m.turningOff, zoneID)

		return
	}

	if m.turningOff == nil {
		m.turningOff = map[int64]turnOff{}
	}

	m.turningOff[zoneID] = turnOffSent
}

// unsignRefusal says why z may not be unsigned now; empty when it may.
// Once its DS allows it, it may still not be written while the session
// is sealed or the Zone Stale, as for any Zone write. While its parent serves a DS, unsigning makes validating resolvers fail
// to resolve it: the DS is removed first, and the Zone unsigned only once
// resolvers may have flushed it.
func (m *Model) unsignRefusal(z dns.Zone) string {
	switch m.dsPresenceOf(z.Name) {
	case dsServed:
		return "DNSSEC stays on: remove the DS record for " + z.Name + " at the registrar first. " +
			"dnscli watches the parent zone for it to go."
	case dsNone:
		if end := m.unsignAfter(z); m.deps.now().Before(end) {
			return "DNSSEC stays on until " + clockText(end, m.deps.now()) + ": resolvers may still hold the DS record of " +
				z.Name + " until then."
		}

		// Unsigning is a Zone write: it passes the write gate (ADR-0001).
		return m.writeRefusal(z)
	case dsChecking:
		return "DNSSEC stays on: dnscli is checking whether the parent zone serves a DS record for " + z.Name +
			". It unsigns the Zone only once it sees none there."
	}

	// A DS Check that failed, or has not answered, is never taken as no
	// DS.
	return "DNSSEC stays on: dnscli could not tell whether the parent zone serves a DS record for " + z.Name +
		". It unsigns the Zone only once it sees none there: Refresh to check again."
}

// dsTTLUnknown is how long resolvers are taken to hold a DS whose TTL
// dnscli never saw, such as one removed while it was not running: a day,
// the TTL of the DS records of the largest TLDs, such as .com.
const dsTTLUnknown = 24 * time.Hour

// unsignAfter is when z may be unsigned: once the TTL its DS was last
// served with has passed since it was first seen gone, so resolvers no
// longer hold it. A DS whose being gone is being stored counts as seen
// gone now.
func (m *Model) unsignAfter(z dns.Zone) time.Time {
	gone := z.DSGoneAt
	if gone.IsZero() {
		gone = m.deps.now()
	}

	ttl := z.DSTTL
	if ttl <= 0 {
		ttl = dsTTLUnknown
	}

	return gone.Add(ttl)
}

// turnOffStep is the next step of a signed Zone being turned off, given
// whether its parent serves a DS: remove it, wait for its TTL once it is
// gone, then unsign.
func (m *Model) turnOffStep(z dns.Zone, ds dsPresence) dnssecStep {
	switch ds {
	case dsServed:
		return stepRemoveDS
	case dsUnknown:
		return stepUnknown
	case dsNone:
		if m.deps.now().Before(m.unsignAfter(z)) {
			return stepWaitUnsign
		}

		return stepUnsign
	}

	return stepSilent
}

// unsignText says a Zone being turned off may be unsigned, and how: through
// the DNSSEC step where the Zone may be written.
func (m *Model) unsignText(z dns.Zone) string {
	if m.writeRefusal(z) != "" {
		return "DNSSEC: DS gone, ready to unsign"
	}

	return "DNSSEC: DS gone, ready to unsign (" + m.keys.keysFor(ActionDNSSECStep) + " turns it off)"
}

// dsSeenMsg reports what was stored of the DS a signed Zone's parent
// serves.
type dsSeenMsg struct {
	zoneID int64
	goneAt time.Time
	ttl    time.Duration
	err    error
}

// noteDSOfName stores what a DS Check of a zone name, by nameKey, saw for
// each Zone of that name.
func (m *Model) noteDSOfName(name string) tea.Cmd {
	var cmds []tea.Cmd

	for _, z := range m.zones {
		if nameKey(z.Name) == name {
			cmds = append(cmds, m.noteDS(m.zoneByID[z.ID]))
		}
	}

	return tea.Batch(cmds...)
}

// noteDS stores what the DS Check of z's zone name saw, when it changes
// what the cache holds: the TTL of the DS set served, or, for a signed
// Zone, the time its DS was first seen gone. A failed check says neither.
func (m *Model) noteDS(z dns.Zone) tea.Cmd {
	c, ok := m.parentDS[nameKey(z.Name)]
	if !ok || c.state != lookupDone || m.liveness(z) == liveNo {
		return nil
	}

	served := len(c.ds) > 0

	switch {
	case served && z.DSGoneAt.IsZero() && z.DSTTL == c.ttl.Truncate(time.Second):
		return nil
	case !served && (!z.DSGoneAt.IsZero() || !m.seenSigned(z)):
		return nil
	}

	ctx, s, at, ttl := m.ctx, m.deps.Store, m.deps.now(), c.ttl

	return func() tea.Msg {
		gone, stored, err := s.SaveDSSeen(ctx, z, served, ttl, at)

		return dsSeenMsg{zoneID: z.ID, goneAt: gone, ttl: stored, err: err}
	}
}

// seenSigned reports whether z is seen signed this session: its keys read,
// or its Provider reporting it signed.
func (m *Model) seenSigned(z dns.Zone) bool {
	return len(m.signingKeysOf(z)) > 0 || signingReported(z)
}

// dsSeen takes what was stored of the DS a signed Zone's parent serves.
func (m *Model) dsSeen(msg dsSeenMsg) {
	if msg.err != nil {
		m.notice = notice{text: "Could not store what was seen of the DS: " + msg.err.Error(), err: true}

		return
	}

	if z, ok := m.zoneByID[msg.zoneID]; ok {
		z.DSGoneAt, z.DSTTL = msg.goneAt, msg.ttl
		m.updateZone(z)
	}
}
