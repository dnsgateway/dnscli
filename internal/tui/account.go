package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// notDisposableWarning is shown when the first Account is added and when a
// passphrase is first enrolled.
const notDisposableWarning = "dnscli keeps Accounts in its local database: it is not a disposable cache. " +
	"Back it up, and keep an Unseal Method enrolled: losing every one makes the stored secrets unrecoverable."

// The first fields of the add-Account dialog; the Provider's credential
// fields follow them, then the Save button.
const (
	addFieldProvider = iota
	addFieldName
	addFieldCreds // the first credential field
)

// addAccountForm is the add-Account dialog, which also edits an Account.
type addAccountForm struct {
	field    int
	reg      dnsprovider.Registry
	types    []string // the registered Provider types
	provider int      // index into types
	name     string
	// creds are the chosen Provider's Fields the user types, in form
	// order: every one but the Tenant, which dnscli finds itself.
	creds []dnsprovider.Field
	// values are what is typed into creds.
	values []string
	err    string
	busy   bool // the Account is being saved
	// editing is the Account being edited; nil when adding one. Its
	// Provider is fixed, and an empty credential field keeps its value.
	editing *dns.Account
	// caret is the focused text field's caret, in runes back from its end.
	caret int
	// picker lists the Provider types; nil when closed.
	picker *picker
	// size is the dialog's size, which it keeps as the user moves
	// through its fields.
	size dialogSize
}

// accountAction runs an insert, edit or delete Action on Accounts.
func (m *Model) accountAction(a Action) tea.Cmd {
	switch a {
	case ActionInsert:
		m.openAddAccount()
	case ActionEdit:
		return m.editAccount()
	case ActionDelete:
		return m.removeAccount()
	}

	return nil
}

// openAddAccount opens the add-Account dialog.
func (m *Model) openAddAccount() {
	m.addForm = &addAccountForm{reg: m.deps.Providers, types: m.deps.Providers.Types()}
	m.addForm.chooseProvider(0)
	m.addForm.size = m.measureAccountForm()
}

// chooseProvider makes types[i] the Account's Provider, with an empty
// input for each of its credential fields.
func (f *addAccountForm) chooseProvider(i int) {
	f.provider = i
	f.creds, f.values = nil, nil

	if i >= len(f.types) {
		return
	}

	for _, fd := range f.reg[f.types[i]].Fields {
		if !fd.Tenant {
			f.creds = append(f.creds, fd)
		}
	}

	f.values = make([]string, len(f.creds))
}

// saveField is the index of the Save button, after the credential fields.
func (f *addAccountForm) saveField() int {
	return addFieldCreds + len(f.creds)
}

// editAccount opens the edit-Account dialog for the highlighted Account,
// which renames it or replaces its API token. Either reseals the token, so
// a sealed session Unseals first.
func (m *Model) editAccount() tea.Cmd {
	a, ok := m.highlightedAccount()
	if !ok {
		return nil
	}

	return m.withSecrets(func() tea.Cmd {
		m.addForm = &addAccountForm{field: addFieldName, reg: m.deps.Providers, types: []string{a.Provider}, name: a.Name, editing: &a}
		m.addForm.chooseProvider(0)
		m.addForm.size = m.measureAccountForm()

		return nil
	})
}

// addAccountKey handles a key while the add- or edit-Account dialog is
// open. As in the Record form, Up, Down and Tab move through the fields
// and the Save button, Left and Right change the Provider, Enter opens its
// list, moves on from a typed field, or saves on the Save button.
func (m *Model) addAccountKey(msg tea.KeyMsg) tea.Cmd {
	f := m.addForm
	a := m.keys.action(msg.String())

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if f.busy {
		return nil
	}

	if f.picker != nil {
		if f.picker.key(a) {
			f.picker = nil
		}

		return nil
	}

	switch i := f.field - addFieldCreds; {
	case f.field == addFieldName:
		if m.keys.typeInto(&f.name, &f.caret, msg) {
			return nil
		}
	case i >= 0 && i < len(f.values):
		if m.keys.typeInto(&f.values[i], &f.caret, msg) {
			return nil
		}
	}

	count := f.saveField() + 1

	switch a {
	case ActionClose:
		m.addForm = nil
	case ActionNextPane, ActionDown:
		f.field = (f.field + 1) % count
	case ActionPrevPane, ActionUp:
		f.field = (f.field + count - 1) % count
	case ActionPaneLeft, ActionPaneRight:
		if f.choosesProvider() {
			step := 1
			if a == ActionPaneLeft {
				step = len(f.types) - 1
			}

			m.chooseAccountProvider((f.provider + step) % len(f.types))
		}
	case ActionSelect:
		switch {
		case f.field == f.saveField() && f.editing != nil:
			return m.submitEditAccount()
		case f.field == f.saveField():
			return m.submitAddAccount()
		case f.choosesProvider():
			f.picker = &picker{title: "Provider", options: f.types, cursor: f.provider, pick: m.chooseAccountProvider}
		default:
			f.field++
		}
	}

	return nil
}

// chooseAccountProvider changes the Provider of the Account being added,
// which changes the credential fields, and so the dialog's size.
func (m *Model) chooseAccountProvider(i int) {
	if i == m.addForm.provider {
		return
	}

	m.addForm.chooseProvider(i)
	m.addForm.size = m.measureAccountForm()
}

// choosesProvider reports whether the focused field is a Provider that
// can be changed: an edited Account keeps its Provider.
func (f *addAccountForm) choosesProvider() bool {
	return f.field == addFieldProvider && f.editing == nil && len(f.types) > 0
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

	if !f.validName(m.accounts, a) {
		return nil
	}

	for i, v := range f.values {
		if v == "" {
			f.err = "Enter the " + f.creds[i].Label + "."
			f.field = addFieldCreds + i

			return nil
		}
	}

	fields := f.typed()

	return m.withSecrets(func() tea.Cmd { return m.saveAccount(a, fields) })
}

// typed is the field map of the credential fields typed into the dialog;
// empty ones are left out.
func (f *addAccountForm) typed() map[string]string {
	fields := map[string]string{}

	for i, v := range f.values {
		if v != "" {
			fields[f.creds[i].Name] = v
		}
	}

	return fields
}

// validName checks the Account name typed into the dialog: a's label must
// be new among accounts, apart from the Account being edited.
func (f *addAccountForm) validName(accounts []dns.Account, a dns.Account) bool {
	switch {
	case a.Name == "":
		f.err = "Enter a name for the Account."
	case strings.ContainsAny(a.Name, ". \t"):
		f.err = "The name cannot contain dots or spaces."
	case slices.ContainsFunc(accounts, func(b dns.Account) bool { return b.ID != a.ID && b.Label() == a.Label() }):
		f.err = a.Label() + " already exists."
	default:
		return true
	}

	f.field = addFieldName

	return false
}

// submitEditAccount validates the edit-Account dialog and saves the
// Account's new name or credentials. Changing neither just closes it.
func (m *Model) submitEditAccount() tea.Cmd {
	f := m.addForm
	f.err = ""

	old := *f.editing
	a := old
	a.Name = strings.TrimSpace(f.name)

	if !f.validName(m.accounts, a) {
		return nil
	}

	typed := f.typed()

	if a.Name == old.Name && len(typed) == 0 {
		m.addForm = nil

		return nil
	}

	return m.withSecrets(func() tea.Cmd { return m.updateAccount(old, a, typed) })
}

// accountEditedMsg reports that an Account was renamed or given new
// credentials.
type accountEditedMsg struct {
	account dns.Account
	err     error
}

// updateAccount stores the edited Account with its credentials sealed for
// its new label: the current ones, opened under the old label, with the
// typed fields replacing theirs. New credentials are verified with the
// Provider first. The credentials are sealed as a field map, so an
// Account still holding an old single API token is rewritten.
func (m *Model) updateAccount(old, a dns.Account, typed map[string]string) tea.Cmd {
	if m.addForm != nil {
		m.addForm.busy = true
	}

	key, store, ctx, providers := m.key, m.deps.Store, m.ctx, m.providers

	return func() tea.Msg {
		fields, err := accountCredentials(ctx, store, key, providers, old)
		if err != nil {
			return accountEditedMsg{account: a, err: err}
		}

		// An Account without an API token is only renamed.
		if fields == nil && len(typed) == 0 {
			return accountEditedMsg{account: a, err: store.UpdateAccountWithSecret(ctx, a, nil)}
		}

		if fields == nil {
			fields = map[string]string{}
		}

		maps.Copy(fields, typed)

		if len(typed) > 0 {
			if err := providers.Verify(ctx, a.Provider, fields); err != nil {
				return accountEditedMsg{account: a, err: notAccepted(err)}
			}
		}

		sealed, err := key.Seal(provider.EncodeCredentials(fields), secretLabel(a))
		if err != nil {
			return accountEditedMsg{account: a, err: err}
		}

		return accountEditedMsg{account: a, err: store.UpdateAccountWithSecret(ctx, a, sealed)}
	}
}

// notAcceptedError reports credentials the Provider refused when they
// were verified.
type notAcceptedError struct{ err error }

func notAccepted(err error) error { return notAcceptedError{err} }

func (e notAcceptedError) Error() string {
	return "The Provider did not accept the credentials: " + e.err.Error()
}

func (e notAcceptedError) Unwrap() error { return e.err }

// accountEdited reports an edit. A saved Account replaces the one this
// session holds at once, not only when the cache reloads: its API token is
// already sealed for its new label, which a Refresh or Apply started
// meanwhile must open it with.
func (m *Model) accountEdited(msg accountEditedMsg) tea.Cmd {
	if msg.err != nil {
		m.accountSaveFailed("Could not save the Account: ", msg.account, msg.err)

		return nil
	}

	for i := range m.accounts {
		if m.accounts[i].ID == msg.account.ID {
			m.accounts[i] = msg.account
		}
	}

	m.addForm = nil
	m.notice = notice{text: "Saved " + msg.account.Label() + "."}

	return m.loadCache
}

// accountSaveFailed shows in the open Account dialog why saving a failed.
func (m *Model) accountSaveFailed(prefix string, a dns.Account, err error) {
	f := m.addForm
	if f == nil {
		return
	}

	f.busy = false
	f.err = prefix + err.Error()

	if errors.As(err, new(notAcceptedError)) {
		f.err = err.Error()
	}

	if errors.Is(err, store.ErrAccountExists) {
		f.err = a.Label() + " already exists."
		f.field = addFieldName
	}
}

// secretLabel binds a sealed API token to its Account.
func secretLabel(a dns.Account) string {
	return "dnscli account secret " + a.Label()
}

// saveAccount verifies the Account's credentials with its Provider, then
// stores the Account with them sealed by the data key, as a field map.
// Credentials the Provider refuses save nothing.
func (m *Model) saveAccount(a dns.Account, fields map[string]string) tea.Cmd {
	if m.addForm != nil {
		m.addForm.busy = true
	}

	key, store, ctx, providers := m.key, m.deps.Store, m.ctx, m.providers

	return func() tea.Msg {
		if err := providers.Verify(ctx, a.Provider, fields); err != nil {
			return accountAddedMsg{account: a, err: notAccepted(err)}
		}

		sealed, err := key.Seal(provider.EncodeCredentials(fields), secretLabel(a))
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
		m.accountSaveFailed("Could not add the Account: ", msg.account, msg.err)

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
	key, store, ctx, providers := m.key, m.deps.Store, m.ctx, m.providers

	return func() tea.Msg {
		n, sync, err := discover(ctx, store, key, providers, a)

		return zonesDiscoveredMsg{account: a, relist: relist, count: n, sync: sync, err: err}
	}
}

// discover lists an Account's Zones from its Provider and makes its cached
// Zones match them. A Provider error leaves the cache untouched, and so
// does an empty list: an API token without zone read permission lists no
// Zones, which must not drop the Account's cache.
func discover(ctx context.Context, s *store.Store, key *vault.Key, providers *provider.Opener, a dns.Account) (int, store.ZoneSync, error) {
	p, fields, err := accountProvider(ctx, s, key, providers, a)
	if err != nil {
		return 0, store.ZoneSync{}, err
	}

	listed, err := p.Zones(ctx, dnsprovider.ZoneFilter{})
	if err != nil || len(listed) == 0 {
		return 0, store.ZoneSync{}, err
	}

	zones := provider.CacheZones(listed)

	expectAccountNameservers(zones, func() ([]string, error) {
		return providers.Nameservers(ctx, p, a.Provider, fields)
	})

	sync, err := s.SyncAccountZones(ctx, a.ID, zones)
	if err != nil {
		return 0, store.ZoneSync{}, err
	}

	return len(zones), sync, nil
}

// expectAccountNameservers gives each Zone the Provider listed without
// nameservers the ones its Account expects. When other Zones were listed
// with nameservers, those are the Account's, provided they all share one
// set; an Account with several sets leaves them unknown. When none was,
// the Account's nameservers are asked, once per session. Not knowing them
// leaves the Live Zone unknown, so a failure here does not fail the
// discovery.
func expectAccountNameservers(zones []dns.Zone, accountNameservers func() ([]string, error)) {
	if !slices.ContainsFunc(zones, func(z dns.Zone) bool { return len(z.Nameservers) == 0 }) {
		return
	}

	var ns []string

	for _, z := range zones {
		switch {
		case len(z.Nameservers) == 0:
		case ns == nil:
			ns = z.Nameservers
		case !slices.Equal(nameSet(z.Nameservers), nameSet(ns)):
			return // several sets: which one a Zone expects is unknown
		}
	}

	if ns == nil {
		var err error
		if ns, err = accountNameservers(); err != nil {
			return
		}
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

	notes := []string{saveRow(f.field == f.saveField())}

	if f.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, f.err, noteWidth)...)
	}

	if f.busy {
		notes = append(notes, f.busyLines()...)
	}

	fields := f.dialogFields(f.name, f.values)
	if f.field < len(fields) {
		fields[f.field].caret = f.caret
	}

	return sizedDialogView(f.title(), fields, f.field, !f.busy, notes, m.accountFormHints(f.field), f.size)
}

// busyLines are the dialog's lines while the Account is being saved.
func (f *addAccountForm) busyLines() []string {
	if f.editing != nil {
		return []string{"", hintStyle.Render("saving…")}
	}

	return []string{"", hintStyle.Render("adding…")}
}

func (f *addAccountForm) title() string {
	if f.editing != nil {
		return "Edit Account " + f.editing.Label()
	}

	return "Add Account"
}

// dialogFields are the rows of the dialog with the name and credential
// values shown; secret ones are masked.
func (f *addAccountForm) dialogFields(name string, values []string) []dialogField {
	provider := "none available"
	if len(f.types) > 0 {
		provider = f.types[f.provider]
	}

	fields := []dialogField{
		{label: "Provider", value: provider, choice: f.editing == nil, fixed: f.editing != nil},
		{label: "Name", value: name},
	}

	for i, fd := range f.creds {
		fields = append(fields, dialogField{label: fd.Label, value: values[i], masked: fd.Secret})
	}

	return fields
}

// accountFormHints are the lines at the bottom of the dialog with field
// focused: what the field is for, and, for the first Account, the warning
// that the database is not disposable.
func (m *Model) accountFormHints(field int) []string {
	f := m.addForm

	var hint string

	switch i := field - addFieldCreds; {
	case field == addFieldProvider:
		hint = "The DNS hosting Provider the Account is at."
		if f.editing != nil {
			hint = "An Account keeps its Provider: add another Account for another Provider."
		}
	case field == addFieldName:
		hint = "The Account shows as <provider>.<name>, such as cloudflare.work; the name has no dots or spaces."
	case i < len(f.creds):
		hint = credentialHint(f.creds[i], f.editing != nil)
	default:
		hint = "Checks the credentials with the Provider, saves the Account, then discovers its Zones."
		if f.editing != nil {
			hint = "Saves the Account's new name or credentials, checking new ones with the Provider."
		}
	}

	hints := wrapNote(hintStyle, hint, noteWidth)

	if len(m.accounts) == 0 {
		hints = append(hints, "")
		hints = append(hints, wrapNote(hintStyle, notDisposableWarning, noteWidth)...)
	}

	return hints
}

// credentialHint is the hint of a credential field.
func credentialHint(fd dnsprovider.Field, editing bool) string {
	switch {
	case editing:
		return "Type a new " + fd.Label + ", or leave it empty to keep the current one."
	case fd.Secret:
		return "The " + fd.Label + " the Provider issued for the Account; dnscli seals it in its local database."
	default:
		return "The " + fd.Label + " the Provider gave the Account; dnscli seals it with the Account's secrets."
	}
}

// measureAccountForm works out the largest size the dialog takes, over
// every focused field, with the values as wide as the dialog shows them.
// The dialog then keeps that size, saving included; only an error grows it.
func (m *Model) measureAccountForm() dialogSize {
	f := m.addForm
	widest := strings.Repeat("W", maxValueWidth)

	values := make([]string, len(f.creds))
	for i := range values {
		values[i] = widest
	}

	fields := f.dialogFields(widest, values)

	if len(f.types) > 0 {
		fields[addFieldProvider].value = slices.MaxFunc(f.types, func(a, b string) int { return len(a) - len(b) })
	}

	var size dialogSize

	for field := range f.saveField() + 1 {
		notes := []string{saveRow(field == f.saveField())}
		if field == f.saveField() {
			notes = append(notes, f.busyLines()...)
		}

		size = size.grow(sizedDialogView(f.title(), fields, field, true, notes, m.accountFormHints(field), dialogSize{}))
	}

	return size
}
