package tui

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	mdns "github.com/miekg/dns"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// keyCheck is what this session read of a Zone's key-signing keys from
// its own nameservers. It is held in memory only, like the DS Check.
type keyCheck struct {
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

// keysCheckedMsg carries the result of one key lookup.
type keysCheckedMsg struct {
	zoneID int64
	keys   []dns.SigningKey
	err    error
}

// checkDNSSEC queues the checks the DNSSEC state of a Zone is worked out
// from: the DS Check of its zone name and the lookup of its keys.
func (m *Model) checkDNSSEC(z dns.Zone) tea.Cmd {
	return tea.Batch(m.checkParentDS(z), m.checkSigningKeys(z))
}

// checkSigningKeys queues a lookup of the key-signing keys a Zone's own
// nameservers serve, unless one is queued or in flight, under the limit
// the Delegation lookups share. A Zone known not to be the Live Zone, and
// one whose nameservers are not known, are not looked up. One its
// Provider reports unsigned is: a DS at its parent then matches none.
func (m *Model) checkSigningKeys(z dns.Zone) tea.Cmd {
	servers := m.nameserversOf(z)

	if m.deps.Resolver == nil || m.liveness(z) == liveNo || len(servers) == 0 {
		return nil
	}

	if m.signingKeys == nil {
		m.signingKeys = map[int64]*keyCheck{}
	}

	c, ok := m.signingKeys[z.ID]

	switch {
	case !ok:
		c = &keyCheck{}
		m.signingKeys[z.ID] = c
	case c.state == lookupQueued || c.state == lookupResolving:
		return nil
	}

	*c = keyCheck{state: lookupQueued, zone: z.Name, servers: servers}
	m.lookups.queue = append(m.lookups.queue, lookupJob{kind: lookupKeys, zoneID: z.ID})

	return m.startLookups()
}

// recordKeysCheck records what a key lookup found, unless its Zone is no
// longer tracked, and looks the keys up again when asked to meanwhile.
func (m *Model) recordKeysCheck(msg keysCheckedMsg) tea.Cmd {
	c, ok := m.signingKeys[msg.zoneID]
	if !ok {
		return nil
	}

	again := c.again

	if msg.err != nil {
		*c = keyCheck{state: lookupFailed, zone: c.zone, servers: c.servers, err: msg.err}
	} else {
		*c = keyCheck{state: lookupDone, zone: c.zone, servers: c.servers, keys: msg.keys}
	}

	if z, ok := m.zoneByID[msg.zoneID]; ok && again {
		return m.checkSigningKeys(z)
	}

	return nil
}

// signingKeysChanged looks the open Zone's keys up again when what they
// are worked out from changed since: its nameservers became known, such
// as when its Delegation resolved after it was opened, or a Refresh
// changed them or its DNSSEC state, such as turning it on. A lookup in
// flight is followed by another once it answers.
func (m *Model) signingKeysChanged(before dns.Zone) tea.Cmd {
	if m.open == nil || m.open.ID != before.ID {
		return nil
	}

	z := *m.open

	c, ok := m.signingKeys[z.ID]

	switch {
	case !ok:
		return m.checkSigningKeys(z)
	case before.Status.DNSSEC == z.Status.DNSSEC && slices.Equal(c.servers, m.nameserversOf(z)):
		return nil
	case c.state == lookupResolving:
		c.again = true

		return nil
	case c.state == lookupQueued:
		c.zone, c.servers = z.Name, m.nameserversOf(z)

		return nil
	}

	return m.checkSigningKeys(z)
}

// keysOf returns the key-signing keys this session read for a Zone; none
// when it read none, or has not read them.
func (m *Model) keysOf(z dns.Zone) []dns.SigningKey {
	if c, ok := m.signingKeys[z.ID]; ok && c.state == lookupDone {
		return c.keys
	}

	return nil
}

// keysError is why this session's lookup of a Zone's key-signing keys
// failed, naming the nameserver; nil when it did not fail.
func (m *Model) keysError(z dns.Zone) error {
	if c, ok := m.signingKeys[z.ID]; ok && c.state == lookupFailed {
		return c.err
	}

	return nil
}

// waitingForKeys is the DNSSEC state of a signed Zone whose nameservers
// do not all serve the same key-signing keys yet.
const waitingForKeys = "DNSSEC: waiting for nameservers to serve the keys"

// dnssecState says, in a few words and its style, the DNSSEC state of z
// for the Zone Status bar, as worked out from its key lookup and the DS
// Check: On when the parent serves a DS that matches one of its keys,
// whoever published it; an error when the parent's DS matches none; the
// DS to publish when the parent serves none. A failed lookup is unknown,
// never off. It is empty when there is nothing to say yet, or for a Zone
// known not to be the Live Zone.
func (m *Model) dnssecState(z dns.Zone) (string, lipgloss.Style) {
	c, ok := m.signingKeys[z.ID]
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

// anyDSMatches reports whether one of the DS records ds, in presentation
// form after the type, fixes one of keys for zone: its digest, of the
// same type, computed from the key, is the same.
func anyDSMatches(zone string, ds []string, keys []dns.SigningKey) bool {
	for _, d := range ds {
		f := strings.Fields(d)
		if len(f) != 4 {
			continue
		}

		digestType, err := strconv.ParseUint(f[2], 10, 8)
		if err != nil {
			continue
		}

		for _, k := range keys {
			rr, err := mdns.NewRR(mdns.Fqdn(zone) + " IN DNSKEY " + k.DNSKEY)
			if err != nil {
				continue
			}

			key, ok := rr.(*mdns.DNSKEY)
			if !ok {
				continue
			}

			want := key.ToDS(uint8(digestType))
			if want != nil && fmt.Sprintf("%d %d %d %s", want.KeyTag, want.Algorithm, want.DigestType, strings.ToUpper(want.Digest)) ==
				strings.Join([]string{f[0], f[1], f[2], strings.ToUpper(f[3])}, " ") {
				return true
			}
		}
	}

	return false
}

// openKeys shows the open Zone's key-signing keys, or says why it cannot:
// a failed lookup says why it failed, naming the nameserver.
func (m *Model) openKeys() {
	if m.open == nil {
		return
	}

	if len(m.keysOf(*m.open)) == 0 && m.keysError(*m.open) == nil {
		m.notice = notice{text: "No DNSSEC keys read from the nameservers of " + m.open.Name + " this session."}

		return
	}

	m.showKeys = true
}

// keysView shows the open Zone's key-signing keys, each as its SHA-256 DS
// and its DNSKEY, in full, on lines of their own to copy into a
// registrar's panel: some registrars take a DS, others the key itself.
func (m *Model) keysView() string {
	z := *m.open

	// The values wrap only when the terminal cannot hold them.
	valueW := max(m.width-8, 20)

	lines := []string{""}

	if err := m.keysError(z); err != nil {
		lines = append(lines, wrapNote(errorStyle, "Could not read the keys from the nameservers of "+z.Name+": "+err.Error()+".", noteWidth)...)
	} else {
		lines = append(lines, wrapNote(lipgloss.NewStyle(), "Publish one of these at the registrar of "+z.Name+": the DS, or the DNSKEY where the registrar takes key data.", noteWidth)...)
	}

	for _, k := range m.keysOf(z) {
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
