package tui

import (
	"fmt"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// freshness is how far a cached Zone has been confirmed in this session.
// It is held in memory only and never persisted.
//
// Only a Validated Zone may be edited. A Serial Check is a hint (ADR-0001),
// so none of its states ever makes a Zone editable. The refreshing,
// validated and Refresh error states are a zoneRefresh, which outranks
// these hints wherever both would show.
type freshness int

const (
	freshUnknown     freshness = iota // no Serial Check has finished or started
	freshChecking                     // a Serial Check is running
	freshHintCurrent                  // the resolver's serial matches the cached one
	freshHintStale                    // the resolver's serial differs from the cached one
	freshError                        // the Serial Check failed
)

// zoneFreshness is what this session knows about one Zone's freshness.
type zoneFreshness struct {
	check freshness
	// liveSerial is the serial the resolver returned.
	liveSerial uint32
	// cachedSerial is the cached serial liveSerial was compared with.
	cachedSerial uint32
	// checkErr is why the Serial Check failed.
	checkErr error
}

// serialCheckedMsg carries the result of one Serial Check.
type serialCheckedMsg struct {
	zoneID int64
	serial uint32
	err    error
}

// zoneQueue schedules background work per Zone, such as Serial Checks or
// Refreshes, with bounded concurrency. Each result message lets the next
// queued Zone start.
type zoneQueue struct {
	queue   []int64 // Zone IDs waiting for their turn
	running int
}

// drop removes a Zone from the queue, such as one that left the cache.
func (q *zoneQueue) drop(id int64) {
	q.queue = slices.DeleteFunc(q.queue, func(queued int64) bool { return queued == id })
}

// queueSerialChecks runs after each cache (re)load. It queues a Serial
// Check for every Zone this session has not checked yet, and again for
// every Zone whose check failed: once per reload, never from a check's own
// result, so a failing resolver is not asked in a loop. A Zone whose cached
// serial changed is compared again with the serial its check returned.
// It starts as many checks as the limit allows.
func (m *Model) queueSerialChecks() tea.Cmd {
	if m.deps.Resolver == nil {
		return nil
	}

	if m.fresh == nil {
		m.fresh = map[int64]*zoneFreshness{}
	}

	for _, z := range m.zones {
		f, ok := m.fresh[z.ID]

		switch {
		case !ok:
			m.fresh[z.ID] = &zoneFreshness{}
			m.checks.queue = append(m.checks.queue, z.ID)
		case f.check == freshError:
			*f = zoneFreshness{}
			m.checks.queue = append(m.checks.queue, z.ID)
		default:
			f.recompare(z.Serial)
		}
	}

	return m.startSerialChecks()
}

// recompare judges a finished Serial Check again against a changed cached
// serial.
func (f *zoneFreshness) recompare(cached uint32) {
	if (f.check == freshHintCurrent || f.check == freshHintStale) && f.cachedSerial != cached {
		*f = compareSerials(f.liveSerial, cached)
	}
}

// compareSerials judges the resolver's serial against the cached one.
func compareSerials(live, cached uint32) zoneFreshness {
	if cached != 0 && live == cached {
		return zoneFreshness{check: freshHintCurrent, liveSerial: live, cachedSerial: cached}
	}

	return zoneFreshness{check: freshHintStale, liveSerial: live, cachedSerial: cached}
}

// startSerialChecks starts queued Serial Checks up to the concurrency limit.
func (m *Model) startSerialChecks() tea.Cmd {
	var cmds []tea.Cmd

	for m.checks.running < m.deps.serialChecks() && len(m.checks.queue) > 0 {
		id := m.checks.queue[0]
		m.checks.queue = m.checks.queue[1:]

		z, cached := m.zoneByID[id]
		f, ok := m.fresh[id]

		if !cached || !ok {
			continue // removed from the cache, or forgotten, meanwhile
		}

		f.check = freshChecking
		m.checks.running++
		cmds = append(cmds, m.serialCheck(z))
	}

	return tea.Batch(cmds...)
}

// serialCheck asks the resolver for a Zone's SOA serial in the background.
func (m *Model) serialCheck(z dns.Zone) tea.Cmd {
	ctx, resolver := m.ctx, m.deps.Resolver

	return func() tea.Msg {
		serial, err := resolver.SOA(ctx, z.Name)

		return serialCheckedMsg{zoneID: z.ID, serial: serial, err: err}
	}
}

// serialChecked records a Serial Check result against the cached serial,
// and starts the next queued check.
func (m *Model) serialChecked(msg serialCheckedMsg) tea.Cmd {
	m.checks.running--

	f, ok := m.fresh[msg.zoneID]
	z, cached := m.zoneByID[msg.zoneID]

	switch {
	case !ok || !cached:
	case msg.err != nil:
		*f = zoneFreshness{check: freshError, checkErr: msg.err}
	default:
		*f = compareSerials(msg.serial, z.Serial)
	}

	return m.startSerialChecks()
}

// freshnessOf returns what this session knows about a Zone's freshness.
func (m *Model) freshnessOf(id int64) zoneFreshness {
	if f, ok := m.fresh[id]; ok {
		return *f
	}

	return zoneFreshness{}
}

var (
	checkingStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	hintCurrentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	hintStaleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

// marker is the one-column freshness marker drawn after a Zone in the
// Domains pane; blank when nothing is known yet.
func (f zoneFreshness) marker() string {
	switch f.check {
	case freshChecking:
		return checkingStyle.Render("…")
	case freshHintCurrent:
		return hintCurrentStyle.Render("≈")
	case freshHintStale:
		return hintStaleStyle.Render("!")
	case freshError:
		return errorStyle.Render("?")
	}

	return " "
}

// describe explains a Zone's freshness in the status bar.
func (f zoneFreshness) describe(cachedSerial uint32) string {
	switch f.check {
	case freshChecking:
		return "checking the serial…"
	case freshHintCurrent:
		return "serial matches (a hint), not validated"
	case freshHintStale:
		if cachedSerial == 0 {
			return fmt.Sprintf("resolver serial %d, none cached: possibly stale", f.liveSerial)
		}

		return fmt.Sprintf("resolver serial %d, cached %d: possibly stale", f.liveSerial, cachedSerial)
	case freshError:
		return "Serial Check failed: " + f.checkErr.Error()
	}

	return ""
}
