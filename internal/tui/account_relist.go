package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// relistZones re-lists the highlighted Account's Zones from its Provider,
// so Zones added or removed in the Provider's console show up. It needs
// the API token, so a sealed session Unseals first.
func (m *Model) relistZones() tea.Cmd {
	a, ok := m.highlightedAccount()
	if !ok {
		return nil
	}

	return m.relistAccount(a)
}

// refreshOrRelist acts on the focused pane: in the Records pane it
// Refreshes the open Zone, and in the Domains and Accounts panes it
// re-lists the Zones of the Account shown or, when no Account is, of every
// Account.
func (m *Model) refreshOrRelist() tea.Cmd {
	switch {
	case m.focus == paneRecords:
		return m.retryRefresh()
	case m.accountFilter != 0:
		a, ok := m.account(m.accountFilter)
		if !ok {
			return nil
		}

		return m.relistAccount(a)
	case len(m.accounts) == 0:
		return nil
	}

	return m.withSecrets(m.relistEveryAccount)
}

// relistBatch is a re-listing of every Account, which reports once, when
// the last of them is back.
type relistBatch struct {
	// waiting holds the IDs of the Accounts not back yet.
	waiting map[int64]bool
	// accounts counts the Accounts re-listed, and added, dropped and
	// replaced their Zones.
	accounts, added, dropped, replaced int
	// problems say, one sentence each, which Accounts were not re-listed
	// or listed no Zones, and why.
	problems []string
}

// relistEveryAccount re-lists the Zones of every Account in an unsealed
// session. An Account that cannot be re-listed now is skipped, and the
// report says why.
func (m *Model) relistEveryAccount() tea.Cmd {
	if m.relistAll != nil {
		m.notice = notice{text: "Already re-listing every Account…"}

		return nil
	}

	b := &relistBatch{waiting: map[int64]bool{}}

	var cmds []tea.Cmd

	for _, a := range m.accounts {
		if refusal := m.relistRefusal(a); refusal != "" {
			b.problems = append(b.problems, refusal)

			continue
		}

		b.waiting[a.ID] = true
		cmds = append(cmds, m.beginRelist(a))
	}

	m.relistAll = b

	if len(cmds) == 0 {
		return m.relistBatchDone()
	}

	m.notice = notice{text: fmt.Sprintf("Re-listing the Zones of %s…", plural(len(cmds), "Account"))}

	return tea.Batch(cmds...)
}

// relistAccount re-lists an Account's Zones from its Provider.
func (m *Model) relistAccount(a dns.Account) tea.Cmd {
	return m.withSecrets(func() tea.Cmd {
		if refusal := m.relistRefusal(a); refusal != "" {
			m.notice = notice{text: refusal, err: true}

			return nil
		}

		m.notice = notice{text: "Re-listing the Zones of " + a.Label() + "…"}

		return m.beginRelist(a)
	})
}

// relistRefusal says why an Account's Zones cannot be re-listed now: it is
// under way, or would race an Offboarding. Empty when they can.
func (m *Model) relistRefusal(a dns.Account) string {
	if m.relisting[a.ID] {
		return a.Label() + " is already being re-listed."
	}

	// A listing taken before the delete would bring the Zone back.
	if m.offboardingIn(a.ID) {
		return "A Zone of " + a.Label() + " is being Offboarded: re-list it once that is done."
	}

	return ""
}

// beginRelist starts re-listing an Account's Zones from its Provider.
func (m *Model) beginRelist(a dns.Account) tea.Cmd {
	if m.relisting == nil {
		m.relisting = map[int64]bool{}
	}

	m.relisting[a.ID] = true

	return m.discoverZones(a, true)
}

// inRelistBatch reports whether a re-listing of Account id is part of the
// re-listing of every Account, and takes it off the batch's wait list.
func (m *Model) inRelistBatch(id int64) bool {
	if m.relistAll == nil || !m.relistAll.waiting[id] {
		return false
	}

	delete(m.relistAll.waiting, id)

	return true
}

// relistBatchDone reports the re-listing of every Account once the last
// Account is back, and reloads the cache once for all of them.
func (m *Model) relistBatchDone() tea.Cmd {
	b := m.relistAll
	if len(b.waiting) > 0 {
		return nil
	}

	m.relistAll = nil

	var text string

	switch accounts := plural(b.accounts, "Account"); {
	case b.accounts == 0:
		text = "No Account was re-listed."
	case b.added == 0 && b.dropped == 0 && b.replaced == 0:
		text = "Re-listed " + accounts + ": no changes."
	case b.replaced == 0:
		text = fmt.Sprintf("Re-listed %s: %s added, %d dropped.", accounts, zoneCount(b.added), b.dropped)
	default:
		text = fmt.Sprintf("Re-listed %s: %s added, %d dropped, %d replaced by a new zone; their cache and changes were reset.",
			accounts, zoneCount(b.added), b.dropped, b.replaced)
	}

	// Problems lead, so a status line cut at the terminal's edge keeps them.
	m.notice = notice{text: strings.Join(append(b.problems, text), " "), err: len(b.problems) > 0}

	return m.loadCache
}

// zonesRelisted reports a re-listing. A failed one changed nothing; a
// successful one also resolves the Account's zone names' Delegation again.
// One that is part of the re-listing of every Account is reported with
// the others, once the last is back.
func (m *Model) zonesRelisted(msg zonesDiscoveredMsg) tea.Cmd {
	delete(m.relisting, msg.account.ID)

	label := msg.account.Label()
	batched := m.inRelistBatch(msg.account.ID)

	if msg.err != nil {
		failure := "Re-listing " + label + " failed: " + msg.err.Error()
		if batched {
			m.relistAll.problems = append(m.relistAll.problems, failure+".")

			return m.relistBatchDone()
		}

		m.notice = notice{text: failure, err: true}

		return nil
	}

	// A replaced Zone keeps its ID but is another zone at the Provider:
	// like a dropped one, it keeps nothing this session built on the old.
	var forget []int64
	for _, z := range slices.Concat(msg.sync.Dropped, msg.sync.Replaced) {
		forget = append(forget, z.ID)
	}

	m.forgetZones(forget)
	m.forgetDelegations(msg.account.ID)

	added, gone, replaced := len(msg.sync.Added), len(msg.sync.Dropped), len(msg.sync.Replaced)

	if batched {
		b := m.relistAll
		b.accounts++
		b.added += added
		b.dropped += gone
		b.replaced += replaced

		if msg.count == 0 {
			b.problems = append(b.problems, label+" listed no Zones, cache kept: check that its API token may list Zones.")
		}

		return m.relistBatchDone()
	}

	switch {
	case msg.count == 0:
		m.notice = notice{text: "Re-listed " + label + ": no Zones listed, cache kept. Check that its API token may list Zones."}
	case added == 0 && gone == 0 && replaced == 0:
		m.notice = notice{text: "Re-listed " + label + ": no changes, " + zoneCount(msg.count) + "."}
	case replaced == 0:
		m.notice = notice{text: fmt.Sprintf("Re-listed %s: %s added, %d dropped.", label, zoneCount(added), gone)}
	default:
		m.notice = notice{text: fmt.Sprintf("Re-listed %s: %s added, %d dropped, %d replaced by a new zone; its cache and changes were reset.", label, zoneCount(added), gone, replaced)}
	}

	return m.loadCache
}
