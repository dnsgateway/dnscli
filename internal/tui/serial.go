package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

// serialQueryFor returns the query for a Zone's serial, at nameserversOf
// it. A Zone whose query failed less than serialQueryBackoff ago has none
// to ask.
func (m *Model) serialQueryFor(z dns.Zone) serialQuery {
	if failed, ok := m.serialFailed[z.ID]; ok && m.deps.now().Before(failed.Add(serialQueryBackoff)) {
		return serialQuery{}
	}

	return serialQuery{resolver: m.deps.Resolver, servers: m.nameserversOf(z)}
}

// nameserversOf returns the nameservers to ask a Zone's own data of, such
// as its serial or its keys: the ones its Provider assigned it, else its
// Delegation as this session resolved it. A zone name in several Accounts
// has no fallback: its Delegation may name another copy's nameservers.
// None when neither is known.
func (m *Model) nameserversOf(z dns.Zone) []string {
	if len(z.Nameservers) > 0 || m.shared[nameKey(z.Name)] {
		return z.Nameservers
	}

	if d, ok := m.delegations[nameKey(z.Name)]; ok && d.state == lookupDone {
		return d.ns
	}

	return nil
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

// serialPollAt are when, after an Apply, the Zone's nameservers are asked
// for its new serial: a Provider that does not report the serial gives
// its nameservers a while to take the changes.
var serialPollAt = []time.Duration{5 * time.Second, 15 * time.Second, 45 * time.Second}

// serialPoll asks a Zone's nameservers for its new serial after an Apply,
// at each of serialPollAt, and stores the first serial that is new:
//
//   - newer (RFC 1982) than the serial cached before the Apply;
//   - with none cached before, one that differs from the first poll's
//     answer, which may be from before or after the Apply; or the first
//     answer itself when the Apply changed nothing, and then it polls once.
//
// An Apply that changed nothing, with a serial cached, is not polled for.
// Until a new serial is stored, the Zone shows its serial as updating, not
// as stale. Quitting stops the polls.
type serialPoll struct {
	query serialQuery
	// before is the serial cached before the Apply; zero when none was.
	before uint32
	// applied is true when the Provider applied any change.
	applied bool
	// first is the first poll's answer, once answered is true.
	first    uint32
	answered bool
	// next is the index into serialPollAt of the next poll.
	next int
}

// serialPolledMsg carries one poll's answer.
type serialPolledMsg struct {
	zoneID int64
	poll   *serialPoll
	// serial is the nameservers' answer; zero when they gave none.
	serial uint32
}

// serialSavedMsg reports the new serial a poll stored.
type serialSavedMsg struct {
	zoneID int64
	serial uint32
	err    error
}

// pollSerial starts polling a Zone's nameservers for its new serial after
// an Apply, from z as cached before it.
func (m *Model) pollSerial(z dns.Zone, applied bool) tea.Cmd {
	q := m.serialQueryFor(z)
	if q.resolver == nil || len(q.servers) == 0 || (!applied && z.Serial != 0) {
		delete(m.serialPolls, z.ID)

		return nil
	}

	if m.serialPolls == nil {
		m.serialPolls = map[int64]*serialPoll{}
	}

	p := &serialPoll{query: q, before: z.Serial, applied: applied}
	m.serialPolls[z.ID] = p

	return m.serialPollCmd(z, p)
}

// serialPollCmd waits for p's next poll and asks the nameservers.
func (m *Model) serialPollCmd(z dns.Zone, p *serialPoll) tea.Cmd {
	wait := serialPollAt[p.next]
	if p.next > 0 {
		wait -= serialPollAt[p.next-1]
	}

	ctx, after, q := m.ctx, m.deps.after, p.query

	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case <-after(wait):
		}

		if ctx.Err() != nil {
			return nil
		}

		serial, _ := q.serial(ctx, z.Name)

		return serialPolledMsg{zoneID: z.ID, poll: p, serial: serial}
	}
}

// serialPolled judges a poll's answer, and stores it or polls again.
func (m *Model) serialPolled(msg serialPolledMsg) tea.Cmd {
	p := m.serialPolls[msg.zoneID]
	z, known := m.zoneByID[msg.zoneID]

	if p == nil || p != msg.poll {
		return nil // superseded by a later Apply
	}

	if !known {
		delete(m.serialPolls, msg.zoneID)

		return nil
	}

	p.next++

	if msg.serial != 0 {
		var isNew bool

		switch {
		case p.before != 0:
			isNew = serialNewer(msg.serial, p.before)
		case !p.applied:
			isNew = true
		case !p.answered:
			p.first, p.answered = msg.serial, true
		default:
			isNew = msg.serial != p.first
		}

		if isNew {
			delete(m.serialPolls, msg.zoneID)

			// A Refresh may have cached a newer serial meanwhile.
			if z.Serial != p.before && !serialNewer(msg.serial, z.Serial) {
				return nil
			}

			return m.saveSerial(z, msg.serial)
		}
	}

	if !p.applied || p.next >= len(serialPollAt) {
		delete(m.serialPolls, msg.zoneID)

		return nil
	}

	return m.serialPollCmd(z, p)
}

// saveSerial stores a Zone's new serial in the background.
func (m *Model) saveSerial(z dns.Zone, serial uint32) tea.Cmd {
	ctx, s := m.ctx, m.deps.Store

	return func() tea.Msg {
		return serialSavedMsg{zoneID: z.ID, serial: serial, err: s.SaveApply(ctx, z, nil, serial)}
	}
}

// serialSaved shows the stored serial. One that could not be stored, such
// as for a Zone forgotten meanwhile, is dropped: a Refresh gets it again.
func (m *Model) serialSaved(msg serialSavedMsg) {
	z, known := m.zoneByID[msg.zoneID]
	if msg.err != nil || !known {
		return
	}

	z.Serial = msg.serial
	m.updateZone(z)
}

// serialUpdating reports whether a Zone's new serial is being polled for
// after an Apply.
func (m *Model) serialUpdating(id int64) bool {
	return m.serialPolls[id] != nil
}
