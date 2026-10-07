package tui

import (
	"context"
	"time"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// serialQueryTimeout bounds an SOA query sent straight to a Zone's
// nameservers, so a silent server holds back a Refresh or an Apply for no
// longer than this.
const serialQueryTimeout = 5 * time.Second

// serialQuery asks a Zone's own authoritative nameservers for its SOA
// serial, for a Provider that does not report it.
type serialQuery struct {
	resolver Resolver
	// servers are the Zone's nameservers; none when not known.
	servers []string
}

// serialQueryBackoff is how long a Zone whose nameservers failed to
// answer is not asked again, so a network that blocks port 53 does not
// hold back every Refresh by serialQueryTimeout.
const serialQueryBackoff = 5 * time.Minute

// serialQueryFor returns the query for a Zone's serial: at the nameservers
// its Provider assigned it, else at its Delegation as this session
// resolved it. A zone name in several Accounts has no fallback: its
// Delegation may name another copy's nameservers. A Zone whose query
// failed less than serialQueryBackoff ago has none to ask.
func (m *Model) serialQueryFor(z dns.Zone) serialQuery {
	if failed, ok := m.serialFailed[z.ID]; ok && m.deps.now().Before(failed.Add(serialQueryBackoff)) {
		return serialQuery{}
	}

	q := serialQuery{resolver: m.deps.Resolver, servers: z.Nameservers}

	if len(q.servers) == 0 && !m.shared[nameKey(z.Name)] {
		if d, ok := m.delegations[nameKey(z.Name)]; ok && d.state == lookupDone {
			q.servers = d.ns
		}
	}

	return q
}

// serial returns the serial the Zone's nameservers answer with; zero when
// there is no resolver or no nameserver to ask, or the query failed, which
// failed reports. A failed query never fails the Refresh or Apply it
// serves: the cached serial is kept.
func (q serialQuery) serial(ctx context.Context, zone string) (serial uint32, failed bool) {
	if q.resolver == nil || len(q.servers) == 0 {
		return 0, false
	}

	ctx, cancel := context.WithTimeout(ctx, serialQueryTimeout)
	defer cancel()

	serial, err := q.resolver.SOAAt(ctx, zone, q.servers)
	if err != nil {
		return 0, true
	}

	return serial, false
}

// serialQueried remembers whether a Zone's nameservers failed to answer
// the query for its serial, as of now.
func (m *Model) serialQueried(zoneID int64, failed bool) {
	if !failed {
		delete(m.serialFailed, zoneID)

		return
	}

	if m.serialFailed == nil {
		m.serialFailed = map[int64]time.Time{}
	}

	m.serialFailed[zoneID] = m.deps.now()
}

// serialNewer reports whether serial a is newer than b in serial number
// arithmetic (RFC 1982), so that a serial that wrapped around counts as
// newer. Any serial is newer than an unknown one, zero.
func serialNewer(a, b uint32) bool {
	if b == 0 {
		return a != 0
	}

	return int32(a-b) > 0 //nolint:gosec // the wrap-around is the arithmetic
}
