package tui

import (
	"fmt"
	"slices"

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

// relistAccount re-lists an Account's Zones from its Provider.
func (m *Model) relistAccount(a dns.Account) tea.Cmd {
	return m.withSecrets(func() tea.Cmd {
		if m.relisting[a.ID] {
			m.notice = notice{text: "Already re-listing " + a.Label() + "…"}

			return nil
		}

		// A listing taken before the delete would bring the Zone back.
		if m.offboardingIn(a.ID) {
			m.notice = notice{text: "A Zone of " + a.Label() + " is being Offboarded: re-list it once that is done.", err: true}

			return nil
		}

		if m.relisting == nil {
			m.relisting = map[int64]bool{}
		}

		m.relisting[a.ID] = true
		m.notice = notice{text: "Re-listing the Zones of " + a.Label() + "…"}

		return m.discoverZones(a, true)
	})
}

// zonesRelisted reports a re-listing. A failed one changed nothing; a
// successful one also resolves the Account's zone names' Delegation again.
func (m *Model) zonesRelisted(msg zonesDiscoveredMsg) tea.Cmd {
	delete(m.relisting, msg.account.ID)

	label := msg.account.Label()

	if msg.err != nil {
		m.notice = notice{text: "Re-listing " + label + " failed: " + msg.err.Error(), err: true}

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

	switch added, gone, replaced := len(msg.sync.Added), len(msg.sync.Dropped), len(msg.sync.Replaced); {
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
