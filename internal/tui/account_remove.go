// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// removeConfirm asks before removing an Account. The user types the
// Account's label to confirm, as removing it cannot be undone.
type removeConfirm struct {
	account dns.Account
	zones   int // how many Zones the Account has cached
	typed   string
	caret   int // in runes back from the end of typed
	err     string
	busy    bool // the Account is being removed
}

// accountRemovedMsg reports the result of removing an Account.
type accountRemovedMsg struct {
	account dns.Account
	err     error
}

// highlightedAccount returns the Account under the cursor of the Accounts
// pane, which the Account actions act on. Outside that pane it explains
// where to pick one.
func (m *Model) highlightedAccount() (dns.Account, bool) {
	if m.focus != paneAccounts || len(m.accounts) == 0 {
		m.notice = notice{text: "Highlight an Account in the Accounts pane first.", err: true}

		return dns.Account{}, false
	}

	return m.accounts[m.cursor[paneAccounts]], true
}

// removeAccount asks to remove the highlighted Account. A sealed session
// is read-only, so it Unseals first.
func (m *Model) removeAccount() tea.Cmd {
	a, ok := m.highlightedAccount()
	if !ok {
		return nil
	}

	return m.withSecrets(func() tea.Cmd {
		m.remove = &removeConfirm{account: a, zones: len(m.zonesOf(a.ID))}

		return nil
	})
}

// zonesOf returns the IDs of an Account's cached Zones.
func (m *Model) zonesOf(accountID int64) []int64 {
	var ids []int64

	for _, z := range m.zones {
		if z.AccountID == accountID {
			ids = append(ids, z.ID)
		}
	}

	return ids
}

// removeAccountKey handles a key while the removal confirmation is open.
func (m *Model) removeAccountKey(msg tea.KeyMsg) tea.Cmd {
	c := m.remove
	a := m.keys.action(msg.String())

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if c.busy || m.keys.typeInto(&c.typed, &c.caret, msg) {
		return nil
	}

	switch a {
	case ActionClose:
		m.remove = nil
	case ActionSelect:
		if c.typed != c.account.Label() {
			c.err = "That is not " + c.account.Label() + ": nothing was removed."
			c.typed = ""

			return nil
		}

		c.busy = true
		c.err = ""
		a, store, ctx := c.account, m.deps.Store, m.ctx

		return func() tea.Msg {
			return accountRemovedMsg{account: a, err: store.RemoveAccount(ctx, a.ID)}
		}
	}

	return nil
}

func (m *Model) accountRemoved(msg accountRemovedMsg) tea.Cmd {
	if msg.err != nil {
		if c := m.remove; c != nil {
			c.busy = false
			c.err = "Could not remove the Account: " + msg.err.Error()
		}

		return nil
	}

	m.remove = nil
	m.forgetZones(m.zonesOf(msg.account.ID))

	if m.accountFilter == msg.account.ID {
		m.setAccount(0)
	}

	m.notice = notice{text: "Removed " + msg.account.Label() + "."}

	return m.loadCache
}

// forgetZones drops what this session knows about Zones that left the
// cache: a later Zone may reuse an ID, so it must not inherit freshness or
// a validated Refresh, and an open one has no Records left.
func (m *Model) forgetZones(ids []int64) {
	for _, id := range ids {
		delete(m.fresh, id)
		m.checks.drop(id)
		delete(m.refreshes, id)
		m.refreshQueue.drop(id)
		delete(m.changesets, id)
		delete(m.applying, id)

		if m.preview != nil && m.preview.zoneID == id {
			m.preview = nil
		}

		if m.recordForm != nil && m.recordForm.zone.ID == id {
			m.recordForm = nil
		}

		if m.templateForm != nil && m.templateForm.zone.ID == id {
			m.templateForm = nil
		}

		if m.zoneEdit != nil && m.zoneEdit.zone.ID == id {
			m.zoneEdit = nil
		}

		if m.offboard != nil && m.offboard.zone.ID == id {
			m.offboard = nil
		}

		if m.open != nil && m.open.ID == id {
			m.open = nil
			m.records = nil
			m.recordsErr = nil
			m.recordsLoading = false
			m.rebuildRecordRows()
		}
	}
}

// zoneCount says how many Zones there are: "no Zones", "1 Zone", "2 Zones".
func zoneCount(n int) string {
	switch n {
	case 0:
		return "no Zones"
	case 1:
		return "1 Zone"
	}

	return fmt.Sprintf("%d Zones", n)
}

func (m *Model) removeAccountView() string {
	c := m.remove
	label := c.account.Label()

	notes := wrapNote(lipgloss.NewStyle(), "Removing "+label+" deletes "+zoneCount(c.zones)+
		", their cached Records and its credentials from this database. Nothing changes at the Provider.", noteWidth)
	notes = append(notes, "")
	notes = append(notes, wrapNote(lipgloss.NewStyle(), "Type "+label+" to confirm.", noteWidth)...)

	if c.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, c.err, noteWidth)...)
	}

	if c.busy {
		notes = append(notes, "", hintStyle.Render("removing…"))
	}

	notes = append(notes, "", hintStyle.Render(m.keys.keysFor(ActionSelect)+" remove · "+m.keys.keysFor(ActionClose)+" cancel"))

	return dialogView("Remove Account", []dialogField{{label: "Account", value: c.typed, caret: c.caret}}, 0, !c.busy, notes)
}
