package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/vault"
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

// accountAddedMsg reports that an Account was saved with its sealed secret.
type accountAddedMsg struct {
	account dns.Account
	err     error
}

// zonesDiscoveredMsg reports the Zones discovered for a new Account.
type zonesDiscoveredMsg struct {
	account dns.Account
	count   int
	err     error
}

// submitAddAccount validates the dialog and saves the Account, after
// Unseal: sealing its API token needs the data key.
func (m *Model) submitAddAccount() tea.Cmd {
	f := m.addForm
	f.err = ""

	if len(f.types) == 0 {
		f.err = "No Provider types are available in this build."

		return nil
	}

	a := dns.Account{Provider: f.types[f.provider], Name: strings.TrimSpace(f.name)}

	switch {
	case a.Name == "":
		f.err = "Enter a name for the Account."
		f.field = addFieldName
	case strings.ContainsAny(a.Name, ". \t"):
		f.err = "The name cannot contain dots or spaces."
		f.field = addFieldName
	case slices.ContainsFunc(m.accounts, func(b dns.Account) bool { return b.Label() == a.Label() }):
		f.err = a.Label() + " already exists."
		f.field = addFieldName
	case f.token == "":
		f.err = "Enter the API token."
		f.field = addFieldToken
	}

	if f.err != "" {
		return nil
	}

	token := f.token

	return m.withSecrets(func() tea.Cmd { return m.saveAccount(a, token) })
}

// secretLabel binds a sealed API token to its Account.
func secretLabel(a dns.Account) string {
	return "dnscli account secret " + a.Label()
}

// saveAccount stores the Account with its API token sealed by the data key.
func (m *Model) saveAccount(a dns.Account, token string) tea.Cmd {
	if m.addForm != nil {
		m.addForm.busy = true
	}

	key, store, ctx := m.key, m.deps.Store, m.ctx

	return func() tea.Msg {
		sealed, err := key.Seal([]byte(token), secretLabel(a))
		if err != nil {
			return accountAddedMsg{err: err}
		}

		a, err = store.AddAccountWithSecret(ctx, a, sealed)

		return accountAddedMsg{account: a, err: err}
	}
}

func (m *Model) accountAdded(msg accountAddedMsg) tea.Cmd {
	if msg.err != nil {
		if m.addForm != nil {
			m.addForm.busy = false
			m.addForm.err = "Could not add the Account: " + msg.err.Error()
		}

		return nil
	}

	m.addForm = nil
	m.notice = notice{text: "Added " + msg.account.Label() + "; discovering its Zones…"}

	return tea.Batch(m.loadCache, m.discoverZones(msg.account))
}

// discoverZones lists an Account's Zones from its Provider in the
// background and caches them.
func (m *Model) discoverZones(a dns.Account) tea.Cmd {
	key, store, ctx, newProvider := m.key, m.deps.Store, m.ctx, m.deps.Providers[a.Provider]

	return func() tea.Msg {
		n, err := discover(ctx, store, key, newProvider, a)

		return zonesDiscoveredMsg{account: a, count: n, err: err}
	}
}

func discover(ctx context.Context, s *store.Store, key *vault.Key, newProvider provider.Constructor, a dns.Account) (int, error) {
	if newProvider == nil {
		return 0, fmt.Errorf("Provider type %q is not available in this build", a.Provider)
	}

	sealed, err := s.AccountSecret(ctx, a.ID)
	if err != nil {
		return 0, err
	}

	token, err := key.Open(sealed, secretLabel(a))
	if err != nil {
		return 0, err
	}

	p, err := newProvider(a, string(token))
	if err != nil {
		return 0, err
	}

	zones, err := p.Zones(ctx)
	if err != nil {
		return 0, err
	}

	for _, z := range zones {
		z.AccountID = a.ID
		if _, err := s.PutZone(ctx, z); err != nil {
			return 0, err
		}
	}

	return len(zones), nil
}

func (m *Model) zonesDiscovered(msg zonesDiscoveredMsg) tea.Cmd {
	if msg.err != nil {
		m.notice = notice{text: "Zone discovery for " + msg.account.Label() + " failed: " + msg.err.Error(), err: true}

		return nil
	}

	m.notice = notice{text: fmt.Sprintf("Discovered %d Zones for %s.", msg.count, msg.account.Label())}

	return m.loadCache
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
		notes = append(notes, wrapNote(hintStyle, notDisposableWarning, noteWidth)...)
	}

	if f.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, f.err, noteWidth)...)
	}

	if f.busy {
		notes = append(notes, "", hintStyle.Render("adding…"))
	}

	notes = append(notes, "", hintStyle.Render(m.keys.keysFor(ActionNextPane)+" next field · "+
		m.keys.keysFor(ActionSelect)+" add · "+m.keys.keysFor(ActionClose)+" cancel"))

	return dialogView("Add Account", fields, f.field, !f.busy, notes)
}
