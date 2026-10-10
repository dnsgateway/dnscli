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

// turnOnText says an unsigned Zone is off, and how to turn it on: through
// the DNSSEC step where its Provider can and the session may write, in
// the Provider's own panel where it cannot.
func (m *Model) turnOnText(z dns.Zone) string {
	a, _ := m.account(z.AccountID)

	switch {
	case !m.accountCan(a, dnsprovider.CapZoneUpdater):
		return "DNSSEC: off (turn it on in " + a.Label() + "'s panel)"
	case m.dnssecWriteRefusal(z) != "":
		return "DNSSEC: off"
	}

	return "DNSSEC: off (" + m.keys.keysFor(ActionDNSSECStep) + " turns it on)"
}

// dnssecWriteRefusal says why z's DNSSEC cannot be changed now, as any
// Zone write: the session is sealed, or the Zone is not a Validated Zone
// (ADR-0001). Empty when it can.
func (m *Model) dnssecWriteRefusal(z dns.Zone) string {
	switch {
	case m.key == nil:
		return "The session is sealed: Unseal it to change the DNSSEC of " + z.Name + "."
	case !m.validatedZone(z.ID):
		return z.Name + " is not validated this session: Refresh it (" + m.keys.keysFor(ActionRefresh) +
			") before changing its DNSSEC."
	}

	return ""
}

// takeDNSSECStep takes the open Zone's next DNSSEC step: turning signing
// on asks to confirm it in the Edit Zone dialog, once the session may
// write the Zone and its Provider can; publishing the DS shows the keys
// to copy. Any other step is only waited for.
func (m *Model) takeDNSSECStep() tea.Cmd {
	if m.open == nil {
		m.notice = notice{text: "Open a Zone first.", err: true}

		return nil
	}

	z := *m.open

	switch m.dnssecStepOf(z) {
	case stepTurnOn:
	case stepPublish:
		m.openSigningKeys()

		return nil
	default:
		m.notice = notice{text: "No DNSSEC step to take for " + z.Name + " now."}

		return nil
	}

	a, ok := m.account(z.AccountID)

	switch {
	case !ok:
		return nil
	case !m.accountCan(a, dnsprovider.CapZoneUpdater):
		m.notice = notice{text: cannotTurnDNSSECOn(a), err: true}

		return nil
	}

	if why := m.dnssecWriteRefusal(z); why != "" {
		m.notice = notice{text: why, err: true}

		return nil
	}

	return m.editZoneOf(z.ID, true)
}

// dnssecRecheckWaits are the waits between re-checks of the open Zone
// while its DNSSEC waits on something outside dnscli: its nameservers to
// serve the keys, or the DS to appear at the parent, however it is
// published. They grow, then go on at the last for as long as the Zone
// waits; each new step, and a Refresh, starts them again.
var dnssecRecheckWaits = []time.Duration{
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
}

// waits reports whether the step waits on something outside dnscli, and
// so is re-checked.
func (s dnssecStep) waits() bool {
	switch s {
	case stepWaitKeys, stepWaitTTL, stepPublish:
		return true
	}

	return false
}

// dnssecWatch re-checks the open Zone's DNSSEC in the background while it
// waits, and wakes the Zone page at the end of the wait before its DS can
// be published, so the next step shows without a Refresh.
type dnssecWatch struct {
	zoneID int64
	// step is the step the re-checks are for.
	step dnssecStep
	// next is the index into dnssecRecheckWaits of the next re-check; it
	// stays at the last.
	next int
	// armed is set while a re-check is waited for.
	armed bool
	// waitEnd is the end of the wait a timer is set for; zero when none.
	waitEnd time.Time
}

// dnssecWatchMsg is a dnssecWatch's timer firing: a re-check when recheck
// is set, else the end of the wait.
type dnssecWatchMsg struct {
	watch   *dnssecWatch
	recheck bool
}

// watchDNSSEC sets the open Zone's DNSSEC timers its step asks for: a
// re-check while it waits for keys or for the DS, unless one is set,
// and a timer for the end of the wait before the DS
// can be published.
func (m *Model) watchDNSSEC() tea.Cmd {
	if m.open == nil {
		m.dnssecWatch = nil

		return nil
	}

	z := *m.open

	w := m.dnssecWatch
	if w == nil || w.zoneID != z.ID {
		w = &dnssecWatch{zoneID: z.ID}
		m.dnssecWatch = w
	}

	step := m.dnssecStepOf(z)
	if !step.waits() {
		return nil
	}

	if step != w.step {
		w.step, w.next = step, 0
	}

	var cmds []tea.Cmd

	if !w.armed {
		w.armed = true
		cmds = append(cmds, m.dnssecTimer(w, dnssecRecheckWaits[w.next], true))
		w.next = min(w.next+1, len(dnssecRecheckWaits)-1)
	}

	if end := m.publishAfter(z); step == stepWaitTTL && !end.Equal(w.waitEnd) {
		w.waitEnd = end
		cmds = append(cmds, m.dnssecTimer(w, end.Sub(m.deps.now()), false))
	}

	return tea.Batch(cmds...)
}

// dnssecTimer waits d on the Clock for w; quitting stops it.
func (m *Model) dnssecTimer(w *dnssecWatch, d time.Duration, recheck bool) tea.Cmd {
	ctx, after := m.ctx, m.deps.after

	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case <-after(d):
		}

		return dnssecWatchMsg{watch: w, recheck: recheck}
	}
}

// dnssecWatched re-checks the open Zone when its watch asks to, unless
// another Zone was opened, or it was Refreshed, meanwhile: the DS at its
// parent, and its keys while they are waited for. The end of the wait
// needs nothing but showing the Zone page again.
func (m *Model) dnssecWatched(msg dnssecWatchMsg) tea.Cmd {
	if msg.watch != m.dnssecWatch || !msg.recheck || m.open == nil {
		return nil
	}

	msg.watch.armed = false
	z := *m.open

	switch step := m.dnssecStepOf(z); {
	case !step.waits():
		return nil
	case step == stepWaitKeys:
		return tea.Batch(m.checkParentDS(z), m.lookUpSigningKeys(z))
	}

	return m.checkParentDS(z)
}
