package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// signingSeenMsg reports what was stored of when a Zone was first seen
// signed.
type signingSeenMsg struct {
	zoneID int64
	at     time.Time
	err    error
}

// noteSigning stores what a Key Lookup of z saw of its signing, when it
// changes what the cache holds: the time it was first seen signed, its
// keys read, or that it was seen unsigned, serving no keys while its
// Provider does not report it signed. A failed lookup, or keys the
// nameservers do not agree on yet, say neither.
func (m *Model) noteSigning(z dns.Zone) tea.Cmd {
	c, ok := m.keyLookups[z.ID]
	if !ok || c.state != lookupDone {
		return nil
	}

	signed := len(c.keys) > 0

	switch {
	case signed && !z.SigningSeenAt.IsZero():
		return nil
	case !signed && (z.SigningSeenAt.IsZero() || signingReported(z)):
		return nil
	}

	ctx, s, at := m.ctx, m.deps.Store, m.deps.now()

	return func() tea.Msg {
		seen, err := s.SaveSigningSeen(ctx, z, signed, at)

		return signingSeenMsg{zoneID: z.ID, at: seen, err: err}
	}
}

// signingReported reports whether z's Provider reports it signed, or
// being signed.
func signingReported(z dns.Zone) bool {
	return z.Status.DNSSEC == dnsprovider.On || z.Status.DNSSEC == dnsprovider.Pending
}

// signingSeen takes what was stored of when a Zone was first seen signed.
func (m *Model) signingSeen(msg signingSeenMsg) {
	if msg.err != nil {
		m.notice = notice{text: "Could not store when the Zone was seen signed: " + msg.err.Error(), err: true}

		return
	}

	if z, ok := m.zoneByID[msg.zoneID]; ok {
		z.SigningSeenAt = msg.at
		m.updateZone(z)
	}
}

// publishAfter is when the DS of the open Zone z may be published: once
// the largest TTL among its cached Records has passed since it was first
// seen signed, so resolvers no longer hold its unsigned data. A Zone
// whose signing is being stored counts as seen signed now.
func (m *Model) publishAfter(z dns.Zone) time.Time {
	seen := z.SigningSeenAt
	if seen.IsZero() {
		seen = m.deps.now()
	}

	var ttl uint32

	if m.open != nil && m.open.ID == z.ID {
		for _, r := range m.records {
			ttl = max(ttl, r.TTL)
		}
	}

	return seen.Add(time.Duration(ttl) * time.Second)
}

// clockText says a time as the user reads it on the Clock: "13:00", with
// the date when it is not today. A time within a minute rounds up, so it
// is never before the time it says.
func clockText(t, now time.Time) string {
	t = t.In(now.Location())
	if r := t.Truncate(time.Minute); !r.Equal(t) {
		t = r.Add(time.Minute)
	}

	if y, mo, d := t.Date(); y != now.Year() || mo != now.Month() || d != now.Day() {
		return t.Format("Jan 2 15:04")
	}

	return t.Format("15:04")
}
