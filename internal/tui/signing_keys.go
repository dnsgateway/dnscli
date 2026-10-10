package tui

import (
	"context"
	"errors"
	"fmt"
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
// Provider's API, for a Key Lookup to prefer over asking its nameservers:
// ok is false when they cannot be read that way, as the session is
// sealed or the Provider has no dnsprovider.KeyReader, when it returns
// none, as it has them only once it signs the Zone, or when the read
// fails, whatever the reason.
func (m *Model) keyReadFor(zoneID int64) func(ctx context.Context) (keys []dns.SigningKey, ok bool) {
	z, found := m.zoneByID[zoneID]
	a, known := m.account(z.AccountID)
	s, key, providers := m.deps.Store, m.key, m.providers

	return func(ctx context.Context) ([]dns.SigningKey, bool) {
		if !found || !known || key == nil {
			return nil, false
		}

		ctx, cancel := context.WithTimeout(ctx, keyReadTimeout)
		defer cancel()

		p, _, err := accountProvider(ctx, s, key, providers, a)
		if err != nil || !dnsprovider.CanReadKeys(p) {
			return nil, false
		}

		keys, err := p.(dnsprovider.KeyReader).SigningKeys(ctx, provider.ModuleZone(z))
		if err != nil || len(keys) == 0 {
			return nil, false
		}

		return provider.SigningKeys(keys), true
	}
}

// recordKeyLookup records what a Key Lookup found, unless its Zone is no
// longer tracked, and looks the keys up again when asked to meanwhile.
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

// signingKeysError is why this session's Key Lookup of a Zone failed,
// naming the nameserver; nil when it did not fail.
func (m *Model) signingKeysError(z dns.Zone) error {
	if c, ok := m.keyLookups[z.ID]; ok && c.state == lookupFailed {
		return c.err
	}

	return nil
}

// waitingForKeys is the DNSSEC state of a signed Zone whose nameservers
// do not all serve the same key-signing keys yet.
const waitingForKeys = "DNSSEC: waiting for nameservers to serve the keys"

// dnssecState says, in a few words and its style, the DNSSEC state of z
// for the Zone Status bar, as worked out from its Key Lookup and the DS
// Check: On when the parent serves a DS that matches one of its keys,
// whoever published it; an error when the parent's DS matches none, or
// the Zone serves no keys; the DS to publish when the parent serves none.
// A failed lookup is unknown, never off. It is empty when there is
// nothing to say yet, or for a Zone known not to be the Live Zone.
func (m *Model) dnssecState(z dns.Zone) (string, lipgloss.Style) {
	c, ok := m.keyLookups[z.ID]
	if !ok || m.liveness(z) == liveNo {
		return "", statusStyle
	}

	switch c.state {
	case lookupQueued, lookupResolving:
		return "", statusStyle
	case lookupFailed:
		if errors.Is(c.err, dns.ErrNameserversDisagree) {
			return waitingForKeys, badgePendingStyle
		}

		return "DNSSEC: unknown", statusStyle
	}

	if len(c.keys) == 0 {
		// A DS at the parent of a Zone that serves no keys fails
		// validation, whatever its Provider reports.
		if m.dsPresenceOf(z.Name) == dsServed {
			return "DNSSEC: DS does not match", errorStyle
		}

		if z.Status.DNSSEC == dnsprovider.On || z.Status.DNSSEC == dnsprovider.Pending {
			return waitingForKeys, badgePendingStyle
		}

		return "", statusStyle
	}

	switch m.dsPresenceOf(z.Name) {
	case dsUnknown:
		return "DNSSEC: unknown", statusStyle
	case dsNone:
		return fmt.Sprintf("DNSSEC: publish the DS (%s shows it)", m.keys.keysFor(ActionShowKeys)), badgePendingStyle
	case dsServed:
		if anyDSMatches(z.Name, m.parentDS[nameKey(z.Name)].ds, c.keys) {
			return "DNSSEC: On", badgeOnStyle
		}

		return "DNSSEC: DS does not match", errorStyle
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

// openSigningKeys shows the open Zone's key-signing keys, or why its Key
// Lookup failed, naming the nameserver; else it says there are none.
func (m *Model) openSigningKeys() {
	if m.open == nil {
		return
	}

	if len(m.signingKeysOf(*m.open)) == 0 && m.signingKeysError(*m.open) == nil {
		m.notice = notice{text: "No DNSSEC keys read for " + m.open.Name + " this session."}

		return
	}

	m.showSigningKeys = true
}

// signingKeysView shows the open Zone's key-signing keys, each as its
// SHA-256 DS and its DNSKEY, in full, on lines of their own to copy into
// a registrar's panel: some registrars take a DS, others the key itself.
// A failed Key Lookup shows why instead.
func (m *Model) signingKeysView() string {
	z := *m.open

	// The values wrap only when the terminal cannot hold them.
	valueW := max(m.width-8, 20)

	lines := []string{""}

	if err := m.signingKeysError(z); err != nil {
		lines = append(lines, wrapNote(errorStyle, "Could not read the keys from the nameservers of "+z.Name+": "+err.Error()+".", noteWidth)...)
	} else {
		lines = append(lines, wrapNote(lipgloss.NewStyle(), "Publish one of these at the registrar of "+z.Name+": the DS, or the DNSKEY where the registrar takes key data.", noteWidth)...)
	}

	for _, k := range m.signingKeysOf(z) {
		lines = append(lines, "")

		if len(k.DS) > 0 {
			lines = append(lines, helpKeyStyle.Render("DS (SHA-256)"))
			lines = append(lines, strings.Split(ansi.Hardwrap(k.DS[0], valueW, false), "\n")...)
		}

		lines = append(lines, helpKeyStyle.Render("DNSKEY"))
		lines = append(lines, strings.Split(ansi.Hardwrap(k.DNSKEY, valueW, false), "\n")...)
	}

	lines = append(lines, "", hintStyle.Render(m.keys.keysFor(ActionClose)+" close"))

	width := 0
	for i, l := range lines {
		lines[i] = " " + l
		width = max(width, lipgloss.Width(lines[i])+1)
	}

	lines = append(lines, "")

	return box("DNSSEC keys for "+z.Name, lines, width+2, len(lines)+2, true)
}
