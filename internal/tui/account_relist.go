package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// relistZones re-lists the highlighted Account's Zones from its Provider,
// so Zones added or removed in the Provider's console show up. It needs
// the API token, so a sealed session Unseals first.
func (m *Model) relistZones() tea.Cmd {
	a, ok := m.highlightedAccount()
	if !ok {
		return nil
	}

	return m.withSecrets(func() tea.Cmd {
		if m.relisting[a.ID] {
			m.notice = notice{text: "Already re-listing " + a.Label() + "…"}

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

// zonesRelisted reports a re-listing. A failed one changed nothing.
func (m *Model) zonesRelisted(msg zonesDiscoveredMsg) tea.Cmd {
	delete(m.relisting, msg.account.ID)

	label := msg.account.Label()

	if msg.err != nil {
		m.notice = notice{text: "Re-listing " + label + " failed: " + msg.err.Error(), err: true}

		return nil
	}

	dropped := make([]int64, len(msg.sync.Dropped))
	for i, z := range msg.sync.Dropped {
		dropped[i] = z.ID
	}

	m.forgetZones(dropped)

	switch added, gone := len(msg.sync.Added), len(msg.sync.Dropped); {
	case msg.count == 0:
		m.notice = notice{text: fmt.Sprintf("Re-listed %s: the Provider listed no Zones, %d dropped. Check that its API token may list Zones.", label, gone)}
	case added == 0 && gone == 0:
		m.notice = notice{text: "Re-listed " + label + ": no changes, " + zoneCount(msg.count) + "."}
	default:
		m.notice = notice{text: fmt.Sprintf("Re-listed %s: %s added, %d dropped.", label, zoneCount(added), gone)}
	}

	return m.loadCache
}
