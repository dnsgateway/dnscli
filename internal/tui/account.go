package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// notDisposableWarning is shown when the first Account is added and when a
// passphrase is first enrolled.
const notDisposableWarning = "dnscli keeps Accounts in its local database: it is not a disposable cache. " +
	"Back it up, and keep an Unseal Method enrolled: losing every one makes the stored secrets unrecoverable."

// Fields of the add-Account dialog.
const (
	addFieldProvider = iota
	addFieldName
	addFieldToken
	addFieldCount
)

// addAccountForm is the add-Account dialog.
type addAccountForm struct {
	field    int
	types    []string // the registered Provider types
	provider int      // index into types
	name     string
	token    string
	err      string
	busy     bool // the Account is being saved
}

// openAddAccount opens the add-Account dialog.
func (m *Model) openAddAccount() {
	m.addForm = &addAccountForm{types: m.deps.Providers.Types()}
}

// addAccountKey handles a key while the add-Account dialog is open.
func (m *Model) addAccountKey(msg tea.KeyMsg) tea.Cmd {
	f := m.addForm
	a := m.keys.action(msg.String())

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if f.busy {
		return nil
	}

	switch f.field {
	case addFieldName:
		if m.keys.typeInto(&f.name, msg) {
			return nil
		}
	case addFieldToken:
		if m.keys.typeInto(&f.token, msg) {
			return nil
		}
	}

	switch a {
	case ActionClose:
		m.addForm = nil
	case ActionNextPane:
		f.field = (f.field + 1) % addFieldCount
	case ActionPrevPane:
		f.field = (f.field + addFieldCount - 1) % addFieldCount
	case ActionUp, ActionDown:
		if f.field == addFieldProvider && len(f.types) > 0 {
			step := 1
			if a == ActionUp {
				step = len(f.types) - 1
			}

			f.provider = (f.provider + step) % len(f.types)
		}
	case ActionSelect:
		if f.field < addFieldCount-1 {
			f.field++

			return nil
		}

		return m.submitAddAccount()
	}

	return nil
}

// submitAddAccount validates the dialog. Saving comes with the next slice.
func (m *Model) submitAddAccount() tea.Cmd {
	return nil
}

func (m *Model) addAccountView() string {
	f := m.addForm

	provider := "none available"
	if len(f.types) > 0 {
		provider = f.types[f.provider]
	}

	fields := []dialogField{
		{label: "Provider", value: provider, choice: true},
		{label: "Name", value: f.name},
		{label: "API token", value: f.token, masked: true},
	}

	var notes []string
	if len(m.accounts) == 0 {
		notes = append(notes, wrapNote(hintStyle, notDisposableWarning, 56)...)
	}

	if f.err != "" {
		notes = append(notes, "", errorStyle.Render(f.err))
	}

	if f.busy {
		notes = append(notes, "", hintStyle.Render("adding…"))
	}

	notes = append(notes, "", hintStyle.Render(m.keys.keysFor(ActionNextPane)+" next field · "+
		m.keys.keysFor(ActionSelect)+" add · "+m.keys.keysFor(ActionClose)+" cancel"))

	return dialogView("Add Account", fields, f.field, !f.busy, notes)
}
