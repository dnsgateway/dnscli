package tui

import (
	"context"
	"errors"
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

// zonesDiscoveredMsg reports the Zones discovered for a new Account, or
// re-listed for an existing one.
type zonesDiscoveredMsg struct {
	account dns.Account
	relist  bool
	count   int // how many Zones the Provider listed
	sync    store.ZoneSync
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

		added, err := store.AddAccountWithSecret(ctx, a, sealed)
		if err != nil {
			return accountAddedMsg{account: a, err: err}
		}

		return accountAddedMsg{account: added}
	}
}

func (m *Model) accountAdded(msg accountAddedMsg) tea.Cmd {
	if msg.err != nil {
		if f := m.addForm; f != nil {
			f.busy = false
			f.err = "Could not add the Account: " + msg.err.Error()

			if errors.Is(msg.err, store.ErrAccountExists) {
				f.err = msg.account.Label() + " already exists."
				f.field = addFieldName
			}
		}

		return nil
	}

	m.addForm = nil
	m.notice = notice{text: "Added " + msg.account.Label() + "; discovering its Zones…"}

	return tea.Batch(m.loadCache, m.discoverZones(msg.account, false))
}

// discoverZones lists an Account's Zones from its Provider in the
// background and caches them. Re-listing an existing Account's Zones runs
// the same discovery; relist changes only how the result is reported.
func (m *Model) discoverZones(a dns.Account, relist bool) tea.Cmd {
	key, store, ctx, newProvider := m.key, m.deps.Store, m.ctx, m.deps.Providers[a.Provider]

	return func() tea.Msg {
		n, sync, err := discover(ctx, store, key, newProvider, a)

		return zonesDiscoveredMsg{account: a, relist: relist, count: n, sync: sync, err: err}
	}
}

// discover lists an Account's Zones from its Provider and makes its cached
// Zones match them. A Provider error leaves the cache untouched, and so
// does an empty list: an API token without zone read permission lists no
// Zones, which must not drop the Account's cache.
func discover(ctx context.Context, s *store.Store, key *vault.Key, newProvider provider.Constructor, a dns.Account) (int, store.ZoneSync, error) {
	p, err := accountProvider(ctx, s, key, newProvider, a)
	if err != nil {
		return 0, store.ZoneSync{}, err
	}

	zones, err := p.Zones(ctx)
	if err != nil || len(zones) == 0 {
		return 0, store.ZoneSync{}, err
	}

	expectAccountNameservers(ctx, p, zones)

	sync, err := s.SyncAccountZones(ctx, a.ID, zones)
	if err != nil {
		return 0, store.ZoneSync{}, err
	}

	return len(zones), sync, nil
}

// expectAccountNameservers gives each Zone the Provider listed without
// nameservers the ones its Account expects, asking the Provider only when
// such a Zone exists. Not knowing them leaves the Live Zone unknown, so a
// failure here does not fail the discovery.
func expectAccountNameservers(ctx context.Context, p provider.Provider, zones []dns.Zone) {
	if !slices.ContainsFunc(zones, func(z dns.Zone) bool { return len(z.Nameservers) == 0 }) {
		return
	}

	ns, err := p.Nameservers(ctx)
	if err != nil {
		return
	}

	for i := range zones {
		if len(zones[i].Nameservers) == 0 {
			zones[i].Nameservers = ns
		}
	}
}

// zonesDiscovered reports a discovery or re-listing. The result for an
// Account removed meanwhile is dropped.
func (m *Model) zonesDiscovered(msg zonesDiscoveredMsg) tea.Cmd {
	if errors.Is(msg.err, store.ErrAccountNotFound) {
		delete(m.relisting, msg.account.ID)

		return nil
	}

	if msg.relist {
		return m.zonesRelisted(msg)
	}

	if msg.err != nil {
		m.notice = notice{text: "Zone discovery for " + msg.account.Label() + " failed: " + msg.err.Error(), err: true}

		return nil
	}

	label := msg.account.Label()

	switch msg.count {
	case 0:
		// An API token without zone read permission lists no Zones.
		m.notice = notice{text: "Discovered no Zones for " + label + ": check that its API token may list Zones."}
	case 1:
		m.notice = notice{text: "Discovered 1 Zone for " + label + "."}
	default:
		m.notice = notice{text: fmt.Sprintf("Discovered %d Zones for %s.", msg.count, label)}
	}

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
