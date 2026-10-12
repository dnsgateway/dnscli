package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// keyLookup is what this session's Key Lookup of a Zone found: the
// key-signing keys its Provider's API returns, or else its own
// nameservers serve. It is held in memory only, like the DS Check.
type keyLookup struct {
	state lookup
	// zone and servers are the zone name and the nameservers asked.
	zone    string
	servers []string
	keys    []dns.SigningKey
	err     error
	// again looks the keys up again once the lookup in flight answers,
	// as what it asked about changed meanwhile.
	again bool
}

// keysLookedUpMsg carries the result of one Key Lookup.
type keysLookedUpMsg struct {
	zoneID int64
	keys   []dns.SigningKey
	err    error
}

// checkDSAndLookUpKeys queues what the DNSSEC state of a Zone is worked
// out from: the DS Check of its zone name and the Key Lookup of the Zone.
func (m *Model) checkDSAndLookUpKeys(z dns.Zone) tea.Cmd {
	return tea.Batch(m.checkParentDS(z), m.lookUpSigningKeys(z))
}

// lookUpSigningKeys queues a Key Lookup of a Zone, unless one is queued
// or in flight, under the limit the Delegation lookups share. A Zone
// known not to be the Live Zone, and one whose nameservers are not known,
// are not looked up. One its Provider reports unsigned is: a DS at its
// parent then matches none.
func (m *Model) lookUpSigningKeys(z dns.Zone) tea.Cmd {
	servers := m.nameserversOf(z)

	if m.deps.Resolver == nil || m.liveness(z) == liveNo || len(servers) == 0 {
		return nil
	}

	if m.keyLookups == nil {
		m.keyLookups = map[int64]*keyLookup{}
	}

	c, ok := m.keyLookups[z.ID]

	switch {
	case !ok:
		c = &keyLookup{}
		m.keyLookups[z.ID] = c
	case c.state == lookupQueued || c.state == lookupResolving:
		return nil
	}

	*c = keyLookup{state: lookupQueued, zone: z.Name, servers: servers}
	m.lookups.push(keysJob(z.ID))

	return m.startLookups()
}

// keyReadTimeout bounds asking a Provider's API for a Zone's keys, so a
// slow one holds back asking its nameservers for no longer than this.
const keyReadTimeout = 10 * time.Second

// keyReadFor returns what reads a Zone's key-signing keys through its
// Provider's API, for a Key Lookup to prefer over asking its nameservers,
// or nil when the session is sealed or the Zone or its Account is gone.
// ok is false when the Provider does not read Signing Keys or returns
// no keys, as it has them only when it signs the Zone, or when the read
// fails, whatever the reason.
func (m *Model) keyReadFor(zoneID int64) func(ctx context.Context) (keys []dns.SigningKey, ok bool) {
	z, found := m.zoneByID[zoneID]
	a, known := m.account(z.AccountID)
	s, key, providers := m.deps.Store, m.key, m.providers

	if !found || !known || key == nil {
		return nil
	}

	return func(ctx context.Context) ([]dns.SigningKey, bool) {
		ctx, cancel := context.WithTimeout(ctx, keyReadTimeout)
		defer cancel()

		p, _, err := accountProvider(ctx, s, key, providers, a)
		if err != nil {
			return nil, false
		}

		details, _ := p.ZoneDetails(ctx, provider.ModuleZone(z), dnsprovider.ZoneDetailRequest{SigningKeys: true})
		if details.Properties.SigningKeys.State != dnsprovider.PropertyRead || len(details.SigningKeys) == 0 {
			return nil, false
		}

		return provider.SigningKeys(details.SigningKeys), true
	}
}

// recordKeyLookup records what a Key Lookup found, unless its Zone is no
// longer tracked, sizes the Zone's Edit Zone dialog for it, and looks the
// keys up again when asked to meanwhile.
func (m *Model) recordKeyLookup(msg keysLookedUpMsg) tea.Cmd {
	c, ok := m.keyLookups[msg.zoneID]
	if !ok {
		return nil
	}

	again := c.again

	if msg.err != nil {
		*c = keyLookup{state: lookupFailed, zone: c.zone, servers: c.servers, err: msg.err}
	} else {
		*c = keyLookup{state: lookupDone, zone: c.zone, servers: c.servers, keys: msg.keys}
	}

	// The Edit Zone dialog of the Zone keeps one size over its rows,
	// whose hints now show the keys read.
	if f := m.zoneEdit; f != nil && f.zone.ID == msg.zoneID {
		f.size = f.size.max(m.measureZoneEdit())
	}

	if z, ok := m.zoneByID[msg.zoneID]; ok && again {
		return m.lookUpSigningKeys(z)
	}

	return nil
}

// signingKeysChanged looks the open Zone's keys up again when what they
// are worked out from changed since before: its nameservers became known,
// such as when its Delegation resolved after it was opened, or a Refresh
// changed them or its DNSSEC state, such as turning it on. A Key Lookup
// in flight is followed by another once it answers.
func (m *Model) signingKeysChanged(before dns.Zone) tea.Cmd {
	if m.open == nil || m.open.ID != before.ID {
		return nil
	}

	z := *m.open

	c, ok := m.keyLookups[z.ID]

	switch {
	case !ok:
		return m.lookUpSigningKeys(z)
	case before.Status.DNSSEC == z.Status.DNSSEC && slices.Equal(c.servers, m.nameserversOf(z)):
		return nil
	case c.state == lookupResolving:
		c.again = true

		return nil
	case c.state == lookupQueued:
		c.zone, c.servers = z.Name, m.nameserversOf(z)

		return nil
	}

	return m.lookUpSigningKeys(z)
}

// signingKeysOf returns the key-signing keys this session read for a
// Zone; none when it read none, or has not read them.
func (m *Model) signingKeysOf(z dns.Zone) []dns.SigningKey {
	if c, ok := m.keyLookups[z.ID]; ok && c.state == lookupDone {
		return c.keys
	}

	return nil
}

// dnssecStep is the DNSSEC state of a Zone, and the next step it offers,
// worked out each time from what can be observed: its Provider's Zone
// Status, its Key Lookup, the DS Check of its parent and when it was
// first seen signed. No step is stored.
type dnssecStep int

const (
	// stepSilent is nothing to say yet, or a Zone known not to be the Live
	// Zone.
	stepSilent dnssecStep = iota
	// stepUnknown is a lookup that failed: never taken as off.
	stepUnknown
	// stepTurnOn is an unsigned Zone whose parent serves no DS: signing
	// may be turned on.
	stepTurnOn
	// stepWaitKeys is a signed Zone whose nameservers do not all serve
	// the same key-signing keys yet.
	stepWaitKeys
	// stepWaitTTL is a signed Zone whose unsigned data resolvers may
	// still hold, or whose largest TTL is not known yet: the DS is
	// published only after publishAfter.
	stepWaitTTL
	// stepPublish is a signed Zone whose DS is to be published.
	stepPublish
	// stepOn is a Zone whose parent serves a DS matching one of its keys.
	stepOn
	// stepMismatch is a parent DS that matches none of the Zone's keys,
	// or a DS for a Zone that serves none: it fails validation.
	stepMismatch
	// stepRemoveDS is a signed Zone being turned off whose parent still
	// serves a DS: it is removed at the registrar first.
	stepRemoveDS
	// stepWaitUnsign is a signed Zone being turned off whose DS is gone
	// from the parent, but may still be held by resolvers: it is unsigned
	// only after unsignAfter.
	stepWaitUnsign
	// stepUnsign is a signed Zone being turned off that may be unsigned.
	stepUnsign
	// stepWaitUnsigned is a Zone turned off whose nameservers still serve
	// its keys.
	stepWaitUnsigned
)

// dnssecStepOf works out z's DNSSEC step: On when the parent serves a DS
// that matches one of its keys, whoever published it; a mismatch when
// the parent's DS matches none, or the Zone serves no keys; else the next
// step towards publishing the DS. A failed lookup is unknown, never off.
func (m *Model) dnssecStepOf(z dns.Zone) dnssecStep {
	c, ok := m.keyLookups[z.ID]
	if !ok || m.liveness(z) == liveNo {
		return stepSilent
	}

	switch c.state {
	case lookupQueued, lookupResolving:
		return stepSilent
	case lookupFailed:
		if errors.Is(c.err, dns.ErrNameserversDisagree) {
			return stepWaitKeys
		}

		return stepUnknown
	}

	ds := m.dsPresenceOf(z.Name)
	off := m.turningOff[z.ID]

	if len(c.keys) == 0 {
		switch {
		case ds == dsServed && off == turnOffAsked:
			// Being turned off, its DS is removed first, whether or not
			// it serves keys.
			return stepRemoveDS
		case ds == dsServed:
			// A DS at the parent of a Zone that serves no keys fails
			// validation, whatever its Provider reports: it is removed.
			return stepMismatch
		case signingReported(z) && off != turnOffSent:
			// A Zone turned off this session may still be reported
			// pending: it is being unsigned, not signed.
			return stepWaitKeys
		case ds == dsNone:
			return stepTurnOn
		case ds == dsUnknown:
			return stepUnknown
		}

		return stepSilent
	}

	switch off {
	case turnOffSent:
		return stepWaitUnsigned
	case turnOffAsked:
		return m.turnOffStep(z, ds)
	}

	switch ds {
	case dsUnknown:
		return stepUnknown
	case dsNone:
		if end, known := m.publishAfter(z, m.recordsOf(z)); !known || m.deps.now().Before(end) {
			return stepWaitTTL
		}

		return stepPublish
	case dsServed:
		if anyDSMatches(z.Name, m.parentDS[nameKey(z.Name)].ds, c.keys) {
			return stepOn
		}

		return stepMismatch
	}

	return stepSilent
}

// dnssecState says, in a few words and its style, the DNSSEC state of z
// and the step it offers, for the Zone Status bar; empty when there is
// nothing to say yet, or for a Zone known not to be the Live Zone.
func (m *Model) dnssecState(z dns.Zone) (string, lipgloss.Style) {
	switch m.dnssecStepOf(z) {
	case stepUnknown:
		return "DNSSEC: unknown", statusStyle
	case stepTurnOn:
		return m.turnOnText(z), statusStyle
	case stepWaitKeys:
		return "DNSSEC: waiting for nameservers to serve the keys", badgePendingStyle
	case stepWaitTTL:
		end, known := m.publishAfter(z, m.recordsOf(z))
		if !known {
			return "DNSSEC: DS can be published once the Records' TTLs are known", badgePendingStyle
		}

		return "DNSSEC: DS can be published after " + clockText(end, m.deps.now()), badgePendingStyle
	case stepPublish:
		return m.dsStepText(z, stepPublish), badgePendingStyle
	case stepOn:
		return "DNSSEC: On", badgeOnStyle
	case stepMismatch:
		// A DS that matches no key fails validation, until a new one is
		// waited for.
		if _, sent := m.dsSentFor(z, stepMismatch); sent {
			return m.dsStepText(z, stepMismatch), badgePendingStyle
		}

		return m.dsStepText(z, stepMismatch), errorStyle
	case stepRemoveDS:
		return m.dsStepText(z, stepRemoveDS), badgePendingStyle
	case stepWaitUnsign:
		return "DNSSEC: unsign after " + clockText(m.unsignAfter(z.DSSeen), m.deps.now()), badgePendingStyle
	case stepUnsign:
		return m.unsignText(z), badgePendingStyle
	case stepWaitUnsigned:
		return "DNSSEC: off, waiting for nameservers to stop serving the keys", badgePendingStyle
	}

	return "", statusStyle
}

// anyDSMatches reports whether one of the DS records ds fixes one of keys
// for zone.
func anyDSMatches(zone string, ds []string, keys []dns.SigningKey) bool {
	for _, d := range ds {
		for _, k := range keys {
			if k.Matches(zone, d) {
				return true
			}
		}
	}

	return false
}

// keyDS is a key-signing key's SHA-256 DS, as the Edit Zone dialog shows
// it; empty when it has none.
func keyDS(k dns.SigningKey) string {
	if len(k.DS) == 0 {
		return ""
	}

	return k.DS[0]
}

// keyDNSKEY is a key-signing key's DNSKEY, as the Edit Zone dialog shows
// it.
func keyDNSKEY(k dns.SigningKey) string { return k.DNSKEY }

// zoneKeys are z's key-signing keys as the Edit Zone dialog's row shows
// them, each as value gives it, or how far its Key Lookup got.
func (m *Model) zoneKeys(z dns.Zone, value func(dns.SigningKey) string) []string {
	c, ok := m.keyLookups[z.ID]
	if !ok {
		c = &keyLookup{}
	}

	return lookupRow(ok, c.state, keyValues(m.signingKeysOf(z), value))
}

// keyValues are keys, each as value gives it, leaving out the empty ones.
func keyValues(keys []dns.SigningKey, value func(dns.SigningKey) string) []string {
	var values []string

	for _, k := range keys {
		if v := value(k); v != "" {
			values = append(values, v)
		}
	}

	return values
}

// keysHint ends the hint of the Edit Zone dialog's key rows: why z's keys
// are not shown, when they are not.
func (m *Model) keysHint(z dns.Zone) string {
	c, ok := m.keyLookups[z.ID]

	switch {
	case !ok:
		return "; they are looked up only for a Zone that may be the Live Zone and whose nameservers are known."
	case c.state == lookupFailed:
		return "; could not read them from its nameservers: " + c.err.Error() + "."
	case c.state == lookupDone && len(c.keys) == 0:
		return "; it has none."
	}

	return "."
}

// keyLines are z's key-signing keys, each as value gives it, in full, on
// lines of their own under the hint of the Edit Zone dialog's key rows,
// to copy: some registrars take a DS, others the key itself. They wrap
// only when the terminal cannot hold them, and line up on the left
// however the dialog centers its hints.
func (m *Model) keyLines(z dns.Zone, value func(dns.SigningKey) string) []string {
	valueW := max(m.width-8, 20)

	values := keyValues(m.signingKeysOf(z), value)
	lines := make([]string, 0, 2*len(values))

	for _, v := range values {
		lines = append(lines, "")
		lines = append(lines, strings.Split(ansi.Hardwrap(v, valueW, false), "\n")...)
	}

	width := 0
	for _, l := range lines {
		width = max(width, lipgloss.Width(l))
	}

	for i, l := range lines {
		lines[i] = l + strings.Repeat(" ", width-lipgloss.Width(l))
	}

	return lines
}
