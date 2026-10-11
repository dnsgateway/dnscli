package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

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
	// attempt counts the dialog's saves; cancel stops the one in flight.
	attempt int
	cancel  context.CancelFunc
	// editing is the Account being edited; nil when adding one. Its
	// Provider is fixed, and an empty credential field keeps its value.
	editing *dns.Account
	// stored are the values the edited Account's credentials hold for
	// creds, in form order; empty where they hold none, or could not be
	// read. Only the ones that are not secret are shown.
	stored []string
	// caret is the focused text field's caret, in runes back from its end.
	caret int
	// picker lists the Provider types, or the Tenants to pick from; nil
	// when closed.
	picker *picker
	// tenant is the Tenant picked for the credentials typed; nil until
	// they reach several and one is picked.
	tenant *dnsprovider.Tenant
	// pickingTenant is set while picker lists the Tenants, and
	// tenantPicked once one is picked: the Account is then saved with it.
	pickingTenant, tenantPicked bool
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

// chooseProvider makes types[i] the Account's Provider, with an input for
// each of its credential fields: empty, or a picked field's default.
func (f *addAccountForm) chooseProvider(i int) {
	f.provider = i
	f.creds, f.values, f.tenant = nil, nil, nil

	if i >= len(f.types) {
		return
	}

	for _, fd := range f.reg[f.types[i]].Fields {
		if !fd.Tenant {
			f.creds = append(f.creds, fd)
		}
	}

	f.values = make([]string, len(f.creds))
	for i, fd := range f.creds {
		if picked(fd) {
			f.values[i] = fd.Default
		}
	}
}

// optional reports whether a credential field may be left empty, as it
// has a default.
func optional(fd dnsprovider.Field) bool {
	return fd.Default != ""
}

// picked reports whether a credential field is picked from its choices
// rather than typed.
func picked(fd dnsprovider.Field) bool {
	return len(fd.Choices) > 0
}

// focusedCred is the index of the focused credential field: negative
// before the credential fields, len(f.creds) or more past them.
func (f *addAccountForm) focusedCred() int {
	return f.field - addFieldCreds
}

// picksCredential reports whether the i-th credential field is picked
// from its choices; false for a typed one, or for i past the credential
// fields.
func (f *addAccountForm) picksCredential(i int) bool {
	return i >= 0 && i < len(f.creds) && picked(f.creds[i])
}

// credentialsChanged forgets the Tenant picked, which other credentials
// may not reach.
func (f *addAccountForm) credentialsChanged() {
	f.tenant = nil
}

// pickCredential sets the i-th credential field to its j-th choice.
func (f *addAccountForm) pickCredential(i, j int) {
	f.values[i] = f.creds[i].Choices[j]
	f.credentialsChanged()
}

// stepCredential moves the i-th credential field step choices on, round
// the list; a field at none of them moves to the first.
func (f *addAccountForm) stepCredential(i, step int) {
	choices := f.creds[i].Choices

	at := slices.Index(choices, f.value(i))
	if at < 0 {
		f.pickCredential(i, 0)

		return
	}

	f.pickCredential(i, (at+step+len(choices))%len(choices))
}

// credentialPicker lists the i-th credential field's choices to pick one,
// starting at its value.
func (f *addAccountForm) credentialPicker(i int) *picker {
	choices := f.creds[i].Choices

	return &picker{
		title: f.creds[i].Label, options: choices, cursor: max(slices.Index(choices, f.value(i)), 0),
		pick: func(j int) { f.pickCredential(i, j) },
	}
}

// saveField is the index of the Save button, after the credential fields.
func (f *addAccountForm) saveField() int {
	return addFieldCreds + len(f.creds)
}

// editAccount opens the edit-Account dialog for the highlighted Account,
// which renames it or replaces its credentials. Either reseals them, so
// a sealed session Unseals first.
func (m *Model) editAccount() tea.Cmd {
	a, ok := m.highlightedAccount()
	if !ok {
		return nil
	}

	return m.editAccountOf(a)
}

// editAccountOf opens the edit-Account dialog for a, once its stored
// credentials are read.
func (m *Model) editAccountOf(a dns.Account) tea.Cmd {
	return m.withSecrets(func() tea.Cmd {
		key, store, ctx, providers := m.key, m.deps.Store, m.ctx, m.providers

		return func() tea.Msg {
			fields, err := accountCredentials(ctx, store, key, providers, a)

			return editAccountMsg{account: a, fields: fields, err: err}
		}
	})
}

// editAccountMsg carries an Account to edit, with its credentials as
// stored, or why they could not be read.
type editAccountMsg struct {
	account dns.Account
	fields  map[string]string
	err     error
}

// openEditAccount opens the edit-Account dialog, showing the stored
// credential fields that are not secret. Credentials that cannot be read
// do not stop it: the Account can still be renamed, or its credentials
// typed anew, with a picked field at its default as when adding.
func (m *Model) openEditAccount(msg editAccountMsg) {
	if errors.Is(msg.err, store.ErrAccountNotFound) {
		return
	}

	a := msg.account
	f := &addAccountForm{field: addFieldName, reg: m.deps.Providers, types: []string{a.Provider}, name: a.Name, editing: &a}
	f.chooseProvider(0)

	f.stored = make([]string, len(f.creds))
	for i, fd := range f.creds {
		f.stored[i] = msg.fields[fd.Name]
		if !fd.Secret && msg.err == nil {
			f.values[i] = f.stored[i]
		}
	}

	if msg.err != nil {
		f.err = "dnscli cannot read the stored credentials: " + msg.err.Error() + ". Type every field to replace them, or save to only rename the Account."
	}

	m.addForm = f
	f.size = m.measureAccountForm()
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
		if a == ActionClose {
			f.cancelSaving()
		}

		return nil
	}

	if f.picker != nil {
		if f.picker.key(a) {
			f.picker = nil
		}

		switch {
		case f.picker != nil || !f.pickingTenant:
		case f.tenantPicked:
			f.pickingTenant = false

			return m.saveWithPickedTenant()
		default:
			f.pickingTenant = false
			f.err = "Pick a Tenant to save the Account: nothing was saved."
		}

		return nil
	}

	switch i := f.focusedCred(); {
	case f.field == addFieldName:
		if m.keys.typeInto(&f.name, &f.caret, msg) {
			return nil
		}
	case i >= 0 && i < len(f.values) && !f.picksCredential(i):
		if m.keys.typeInto(&f.values[i], &f.caret, msg) {
			f.credentialsChanged()

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
		step := 1
		if a == ActionPaneLeft {
			step = -1
		}

		switch {
		case f.choosesProvider():
			m.chooseAccountProvider((f.provider + step + len(f.types)) % len(f.types))
		case f.picksCredential(f.focusedCred()):
			f.stepCredential(f.focusedCred(), step)
		}
	case ActionSelect:
		switch {
		case f.field == f.saveField() && f.editing != nil:
			return m.submitEditAccount()
		case f.field == f.saveField():
			return m.submitAddAccount()
		case f.choosesProvider():
			f.picker = &picker{title: "Provider", options: f.types, cursor: f.provider, pick: m.chooseAccountProvider}
		case f.picksCredential(f.focusedCred()):
			f.picker = f.credentialPicker(f.focusedCred())
		default:
			f.field++
		}
	}

	return nil
}

// pickTenant opens the list of the Tenants the credentials reach, to
// pick the one the Account acts within.
func (f *addAccountForm) pickTenant(tenants []dnsprovider.Tenant) {
	f.err = "The credentials reach several Tenants: pick the one the Account acts within."
	f.pickingTenant = true
	f.picker = &picker{title: "Tenant", options: tenantOptions(tenants), pick: func(i int) {
		f.tenant = &tenants[i]
		f.tenantPicked = true
	}}
}

// saveWithPickedTenant saves the Account with the Tenant just picked.
// With no name typed, the Tenant's is suggested instead, for the user to
// save or edit.
func (m *Model) saveWithPickedTenant() tea.Cmd {
	f := m.addForm
	f.tenantPicked = false

	if strings.TrimSpace(f.name) == "" && f.editing == nil {
		if name := tenantAccountName(*f.tenant); name != "" {
			f.suggestName(name)

			return nil
		}
	}

	if f.editing != nil {
		return m.submitEditAccount()
	}

	return m.submitAddAccount()
}

// suggestName fills the Name field with name, suggested after the
// Account's Tenant, and moves to the Save button, which accepts it.
func (f *addAccountForm) suggestName(name string) {
	f.name, f.caret = name, 0
	f.field = f.saveField()
	f.err = "Suggested " + dns.Account{Provider: f.types[f.provider], Name: name}.Label() +
		" after its Tenant: save to accept it, or edit the name."
}

// tenantAccountName is the Account name suggested for a Tenant: its name
// in lower case, with each run of other characters than letters and
// digits a hyphen, and apostrophes left out. Cloudflare names a user's
// own account "<email>'s Account": the suggestion is then the email's.
// It is empty for a Tenant without a name.
func tenantAccountName(t dnsprovider.Tenant) string {
	name := strings.ToLower(strings.TrimSpace(t.Name))

	for _, suffix := range []string{"'s account", "’s account"} {
		if base, ok := strings.CutSuffix(name, suffix); ok && strings.TrimSpace(base) != "" {
			name = base
		}
	}

	var b strings.Builder

	hyphen := false

	for _, r := range name {
		switch {
		case r == '\'' || r == '’':
			continue
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			hyphen = b.Len() > 0

			continue
		}

		if hyphen {
			b.WriteByte('-')
			hyphen = false
		}

		b.WriteRune(r)
	}

	return b.String()
}

// tenantField is the name of the field the Provider of the Account in
// the dialog takes its Tenant in; empty when it takes none.
func (f *addAccountForm) tenantField() string {
	if f == nil || f.provider >= len(f.types) {
		return ""
	}

	return provider.TenantField(f.reg[f.types[f.provider]])
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
	attempt int // the dialog's save attempt
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
// Unseal: sealing its credentials needs the data key.
func (m *Model) submitAddAccount() tea.Cmd {
	f := m.addForm
	f.err = ""

	if len(f.types) == 0 {
		f.err = "No Provider types are available in this build."

		return nil
	}

	a := dns.Account{Provider: f.types[f.provider], Name: strings.TrimSpace(f.name)}

	// Left empty, the name is the Tenant's, once the credentials name it.
	if (a.Name != "" || f.tenantField() == "") && !f.validName(m.accounts, a) {
		return nil
	}

	if !f.complete() {
		return nil
	}

	fields := f.typed()

	return m.withSecrets(func() tea.Cmd { return m.saveAccount(a, fields) })
}

// value is what is typed into the i-th credential field, without
// surrounding whitespace; a blank field is empty.
func (f *addAccountForm) value(i int) string {
	return strings.TrimSpace(f.values[i])
}

// typed is the field map of the credential fields typed into the dialog;
// empty ones are left out.
func (f *addAccountForm) typed() map[string]string {
	fields := map[string]string{}

	for i := range f.values {
		if v := f.value(i); v != "" {
			fields[f.creds[i].Name] = v
		}
	}

	if name := f.tenantField(); f.tenant != nil && name != "" {
		fields[name] = f.tenant.ID
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

	if a.Name == old.Name && !f.changed() {
		m.addForm = nil

		return nil
	}

	if f.changed() && !f.complete() {
		return nil
	}

	typed, changed := f.typed(), f.changed()

	return m.withSecrets(func() tea.Cmd { return m.updateAccount(old, a, typed, changed) })
}

// changed reports whether a credential field holds a value other than
// the stored one; an empty one keeps it, and so does an optional one at
// its default where none is stored.
func (f *addAccountForm) changed() bool {
	if f.tenant != nil {
		return true
	}

	for i, fd := range f.creds {
		v := f.value(i)
		if v != "" && v != f.stored[i] && (f.stored[i] != "" || v != fd.Default) {
			return true
		}
	}

	return false
}

// complete checks that each required credential field has a value,
// typed or stored, else focuses the first without one. A field with a
// default is optional: left empty, it takes its default.
func (f *addAccountForm) complete() bool {
	for i, fd := range f.creds {
		if !optional(fd) && f.value(i) == "" && (f.editing == nil || f.stored[i] == "") {
			f.err = "Enter the " + fd.Label + "."
			f.field = addFieldCreds + i

			return false
		}
	}

	return true
}

// accountEditedMsg reports that an Account was renamed or given new
// credentials.
type accountEditedMsg struct {
	account dns.Account
	attempt int // the dialog's save attempt
	err     error
}

// updateAccount stores the edited Account with its credentials sealed for
// its new label: the stored ones, opened under the old label, with the
// typed fields replacing theirs. Changed credentials are verified with the
// Provider first; typed anew in full, they need not be read. Credentials
// readable as a field map are sealed as one, so an Account still holding
// an old single API token is rewritten; ones that are not are only
// resealed for a rename.
func (m *Model) updateAccount(old, a dns.Account, typed map[string]string, changed bool) tea.Cmd {
	key, store, providers := m.key, m.deps.Store, m.providers
	ctx, cancel, attempt := m.startSaving()
	required, secrets, tenantField := m.addForm.requiredFields(), m.addForm.secretFields(), m.addForm.tenantField()

	return func() tea.Msg {
		defer cancel()

		sealed, err := store.AccountSecret(ctx, old.ID)
		if err != nil {
			return accountEditedMsg{account: a, attempt: attempt, err: err}
		}

		var plain []byte

		var fields map[string]string

		if sealed != nil {
			if plain, err = key.Open(sealed, secretLabel(old)); err == nil {
				fields, err = providers.Credentials(old.Provider, plain)
			}
		}

		switch {
		case !changed && sealed == nil:
			// An Account without credentials is only renamed.
			return accountEditedMsg{account: a, attempt: attempt, err: saveIfLive(ctx, func() error { return store.UpdateAccountWithSecret(ctx, a, nil) })}
		case !changed && fields != nil:
			// Read as a field map, they are sealed as one.
			plain = provider.EncodeCredentials(fields)
		case !changed && plain == nil:
			// Credentials that cannot be opened cannot be resealed.
			return accountEditedMsg{account: a, attempt: attempt, err: err}
		case !changed:
			// Opened but not readable as fields: resealed as they are.
		default:
			// Stored fields that cannot be read are replaced in full.
			if fields == nil {
				fields = map[string]string{}
			}

			// Another secret may reach other Tenants: the stored one is
			// found again, unless one was just picked for it.
			if slices.ContainsFunc(secrets, func(name string) bool { return typed[name] != "" && typed[name] != fields[name] }) {
				delete(fields, tenantField)
			}

			maps.Copy(fields, typed)

			for _, name := range required {
				if fields[name] == "" {
					if err == nil {
						err = fmt.Errorf("the credentials have no %s", name)
					}

					return accountEditedMsg{account: a, attempt: attempt, err: err}
				}
			}

			if fields, _, err = verifyCredentials(ctx, providers, a.Provider, fields); err != nil {
				return accountEditedMsg{account: a, attempt: attempt, err: err}
			}

			plain = provider.EncodeCredentials(fields)
		}

		resealed, err := key.Seal(plain, secretLabel(a))
		if err != nil {
			return accountEditedMsg{account: a, attempt: attempt, err: err}
		}

		return accountEditedMsg{account: a, attempt: attempt, err: saveIfLive(ctx, func() error { return store.UpdateAccountWithSecret(ctx, a, resealed) })}
	}
}

// requiredFields are the names of the credential fields the dialog asks
// for that have no default, each of which the credentials must hold.
func (f *addAccountForm) requiredFields() []string {
	if f == nil {
		return nil
	}

	var names []string

	for _, fd := range f.creds {
		if !optional(fd) {
			names = append(names, fd.Name)
		}
	}

	return names
}

// secretFields are the names of the secret credential fields the dialog
// asks for.
func (f *addAccountForm) secretFields() []string {
	if f == nil {
		return nil
	}

	var names []string

	for _, fd := range f.creds {
		if fd.Secret {
			names = append(names, fd.Name)
		}
	}

	return names
}

// startSaving marks the dialog busy saving, and returns the context the
// save runs in, which Esc cancels, its cancel func, and the attempt it is.
func (m *Model) startSaving() (context.Context, context.CancelFunc, int) {
	ctx, cancel := context.WithCancel(m.ctx)

	f := m.addForm
	if f == nil {
		return ctx, cancel, 0
	}

	f.busy = true
	f.attempt++
	f.cancel = cancel

	return ctx, cancel, f.attempt
}

// cancelSaving stops the save in flight, which saves nothing, and returns
// to the dialog. The canceled attempt's failure is not shown.
func (f *addAccountForm) cancelSaving() {
	if f.cancel != nil {
		f.cancel()
	}

	f.busy = false
	f.attempt++
	f.err = "Canceled: nothing was saved."
}

// saveIfLive runs save unless ctx ended, as when the user canceled
// while the credentials were verified.
func saveIfLive(ctx context.Context, save func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return save()
}

// verifyCredentials asks the Provider whether it accepts the credentials.
// Fields its Provider cannot be built from are refused without asking it,
// and a Provider that could not be reached, as when listing the Tenants
// failed, has not refused them.
func verifyCredentials(ctx context.Context, providers *provider.Opener, typ string, fields map[string]string) (map[string]string, dnsprovider.Tenant, error) {
	scoped, tenant, err := providers.Verify(ctx, typ, fields)
	if err == nil || ctx.Err() != nil || errors.As(err, new(*provider.InvalidCredentialsError)) {
		return scoped, tenant, err
	}

	if unreachable(err) {
		return nil, tenant, unreachableError{err}
	}

	return nil, tenant, notAccepted(err)
}

// unreachable reports whether err says nothing of the credentials, as the
// Provider could not answer: a network failure, a rate limit or a server
// error. Anything else the Provider answered is a refusal.
func unreachable(err error) bool {
	var apiErr *dnsprovider.APIError

	switch {
	case errors.Is(err, dnsprovider.ErrRateLimited):
		return true
	case errors.As(err, &apiErr):
		return apiErr.StatusCode >= 500
	default:
		return !errors.Is(err, dnsprovider.ErrUnauthorized) && !errors.Is(err, dnsprovider.ErrNotFound) &&
			!errors.Is(err, dnsprovider.ErrConflict)
	}
}

// unreachableError reports credentials that could not be verified as the
// Provider could not be reached.
type unreachableError struct{ err error }

func (e unreachableError) Error() string {
	return "Could not reach the Provider: " + e.err.Error()
}

func (e unreachableError) Unwrap() error { return e.err }

// notAcceptedError reports credentials the Provider refused when they
// were verified.
type notAcceptedError struct{ err error }

func notAccepted(err error) error { return notAcceptedError{err} }

func (e notAcceptedError) Error() string {
	return "The Provider did not accept the credentials: " + e.err.Error()
}

func (e notAcceptedError) Unwrap() error { return e.err }

// accountEdited reports an edit. A saved Account replaces the one this
// session holds at once, not only when the cache reloads: its credentials
// are already sealed for its new label, which a Refresh or Apply started
// meanwhile must open them with.
func (m *Model) accountEdited(msg accountEditedMsg) tea.Cmd {
	if msg.err != nil {
		m.accountSaveFailed("Could not save the Account: ", msg.account, msg.attempt, msg.err)

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

// errNoAccountName is an Account added without a name, whose credentials
// name no Tenant to name it after.
var errNoAccountName = errors.New("enter a name for the Account")

// nameSuggestion stops saving an Account added without a name, whose
// credentials act within tenant: its name is suggested from it.
type nameSuggestion struct{ tenant dnsprovider.Tenant }

func (nameSuggestion) Error() string { return "the Account is named after its Tenant" }

// accountSaveFailed shows in the open Account dialog why its save attempt
// failed; a canceled attempt has nothing to show.
func (m *Model) accountSaveFailed(prefix string, a dns.Account, attempt int, err error) {
	f := m.addForm
	if f == nil || attempt != f.attempt {
		return
	}

	f.busy = false
	f.err = prefix + err.Error()

	var (
		invalid *provider.InvalidCredentialsError
		choice  *provider.TenantChoiceError
		suggest nameSuggestion
	)

	switch {
	case errors.As(err, &suggest):
		f.tenant = &suggest.tenant
		f.suggestName(tenantAccountName(suggest.tenant))
	case errors.As(err, &choice):
		f.pickTenant(choice.Tenants)
	case errors.Is(err, errNoAccountName):
		f.err = "Enter a name for the Account."
		f.field = addFieldName
	case errors.As(err, new(notAcceptedError)), errors.As(err, new(unreachableError)):
		f.err = err.Error()
	case errors.As(err, &invalid):
		f.err = "The credentials are not valid: " + invalid.Err.Error()
	}

	if errors.Is(err, store.ErrAccountExists) {
		f.err = a.Label() + " already exists."
		f.field = addFieldName
	}
}

// secretLabel binds sealed credentials to their Account.
func secretLabel(a dns.Account) string {
	return "dnscli account secret " + a.Label()
}

// saveAccount verifies the Account's credentials with its Provider, then
// stores the Account with them sealed by the data key, as a field map.
// Credentials the Provider refuses save nothing, nor does a canceled save.
func (m *Model) saveAccount(a dns.Account, fields map[string]string) tea.Cmd {
	key, store, providers := m.key, m.deps.Store, m.providers
	ctx, cancel, attempt := m.startSaving()

	return func() tea.Msg {
		defer cancel()

		fields, tenant, err := verifyCredentials(ctx, providers, a.Provider, fields)
		if err != nil {
			return accountAddedMsg{account: a, attempt: attempt, err: err}
		}

		if a.Name == "" {
			if tenantAccountName(tenant) == "" {
				return accountAddedMsg{account: a, attempt: attempt, err: errNoAccountName}
			}

			// The Tenant's name is suggested, not saved unseen.
			return accountAddedMsg{account: a, attempt: attempt, err: nameSuggestion{tenant}}
		}

		sealed, err := key.Seal(provider.EncodeCredentials(fields), secretLabel(a))
		if err != nil {
			return accountAddedMsg{account: a, attempt: attempt, err: err}
		}

		var added dns.Account

		err = saveIfLive(ctx, func() error {
			added, err = store.AddAccountWithSecret(ctx, a, sealed)

			return err
		})
		if err != nil {
			return accountAddedMsg{account: a, attempt: attempt, err: err}
		}

		return accountAddedMsg{account: added, attempt: attempt}
	}
}

func (m *Model) accountAdded(msg accountAddedMsg) tea.Cmd {
	if msg.err != nil {
		m.accountSaveFailed("Could not add the Account: ", msg.account, msg.attempt, msg.err)

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
	reach, err := openReach(ctx, s, key, providers, a)
	if err != nil {
		return 0, store.ZoneSync{}, err
	}

	if reach.everyErr != nil {
		// Its own Tenant's Zones alone would drop the others' from the
		// cache.
		return 0, store.ZoneSync{}, fmt.Errorf("listing the Tenants its credentials reach: %w", reach.everyErr)
	}

	listed, err := reach.own.ListZones(ctx, dnsprovider.ZoneFilter{})
	if err != nil {
		return 0, store.ZoneSync{}, err
	}

	own := provider.CacheZones(listed.Zones)

	expectAccountNameservers(own, func() ([]string, error) {
		return providers.Nameservers(ctx, reach.own, a.Provider, reach.fields)
	})

	zones := own

	if reach.every != nil {
		// The Account's Zones are those of every Tenant it reaches, its
		// own Tenant's copy of a zone name kept over another's.
		listed, err = reach.every.ListZones(ctx, dnsprovider.ZoneFilter{})
		if err != nil {
			return 0, store.ZoneSync{}, err
		}

		zones = append(zones, othersZones(own, provider.CacheZones(listed.Zones))...)
	}

	if len(zones) == 0 {
		return 0, store.ZoneSync{}, nil
	}

	cached, err := cachedZoneIDs(ctx, s, a.ID)
	if err != nil {
		return 0, store.ZoneSync{}, err
	}

	zones = servedZones(zones, cached)

	sync, err := s.SyncAccountZones(ctx, a.ID, zones)
	if err != nil {
		return 0, store.ZoneSync{}, err
	}

	return len(zones), sync, nil
}

// othersZones are the Zones listed of every Tenant whose names own, the
// Zones of the Account's own Tenant, do not have.
func othersZones(own, every []dns.Zone) []dns.Zone {
	names := map[string]bool{}
	for _, z := range own {
		names[nameKey(z.Name)] = true
	}

	return slices.DeleteFunc(every, func(z dns.Zone) bool { return names[nameKey(z.Name)] })
}

// servedZones keeps one Zone per zone name, as the cache holds them, in
// the order the names were first listed. A zone name can be listed more
// than once: an unscoped Cloudflare token lists it once per Tenant holding
// it, and a single Tenant can hold a Moved copy next to one Onboarded
// again. Names compare as DNS names, by nameKey (the module's ZoneName
// rule), and are kept in that form.
//
// It keeps the Active copy, else the Pending one, else any other. This
// mirrors the preference of the module's Cloudflare Zone(name)
// (providers/cloudflare in github.com/dnsgateway/dnsprovider), which the
// module does not export for a list; keep the two in step. Unlike Zone(name),
// which falls back to listing order, a tie keeps the copy cached as
// cachedIDs[name], else the smallest Provider zone ID, so a listing in
// another order does not swap the cached Zone.
func servedZones(zones []dns.Zone, cachedIDs map[string]string) []dns.Zone {
	rank := func(s dnsprovider.HostingState) int {
		switch s {
		case dnsprovider.HostingActive:
			return 0
		case dnsprovider.HostingPending:
			return 1
		default:
			return 2
		}
	}

	// better reports whether z is preferred to the copy kept so far.
	better := func(z, kept dns.Zone) bool {
		if r, k := rank(z.State), rank(kept.State); r != k {
			return r < k
		}

		if cached, ok := cachedIDs[z.Name]; ok && (cached == kept.ProviderID || cached == z.ProviderID) {
			return cached == z.ProviderID
		}

		return z.ProviderID < kept.ProviderID
	}

	at := map[string]int{}
	out := zones[:0:0]

	for _, z := range zones {
		z.Name = nameKey(z.Name)

		i, seen := at[z.Name]
		if !seen {
			at[z.Name] = len(out)
			out = append(out, z)

			continue
		}

		if better(z, out[i]) {
			out[i] = z
		}
	}

	return out
}

// cachedZoneIDs returns the Provider zone ID of each of an Account's
// cached Zones, by nameKey.
func cachedZoneIDs(ctx context.Context, s *store.Store, accountID int64) (map[string]string, error) {
	zones, err := s.Zones(ctx)
	if err != nil {
		return nil, err
	}

	out := map[string]string{}

	for _, z := range zones {
		if z.AccountID == accountID {
			out[nameKey(z.Name)] = z.ProviderID
		}
	}

	return out, nil
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

		if m.inRelistBatch(msg.account.ID) {
			return m.relistBatchDone()
		}

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

// tenantUse runs the discovery or re-listing again for the Account as it
// is by then.
func (msg zonesDiscoveredMsg) tenantUse(m *Model) (int64, func() tea.Cmd, error) {
	id, relist := msg.account.ID, msg.relist

	return id, func() tea.Cmd {
		a, ok := m.account(id)

		switch {
		case !ok:
			return nil
		case relist:
			return m.relistAccount(a)
		default:
			return m.discoverZones(a, false)
		}
	}, msg.err
}

func (m *Model) addAccountView() string {
	f := m.addForm

	var notes []string

	if f.err != "" {
		notes = wrapNote(errorStyle, f.err, noteWidth)
	}

	fields := f.dialogFields(f.name, f.values)
	if f.field < len(fields) {
		fields[f.field].caret = f.caret
	}

	return sizedDialogView(f.title(), fields, f.field, !f.busy, notes, m.accountFormFooter(f.field, f.busy), f.size)
}

// accountFormFooter is the bottom of the dialog with field focused,
// centered: its hints, then the Save button, and while busy says the
// Account is being saved.
func (m *Model) accountFormFooter(field int, busy bool) []string {
	f := m.addForm

	footer := append(m.accountFormHints(field), "", saveRow(field == f.saveField()))
	if busy {
		footer = append(footer, f.busyLines()...)
	}

	return footer
}

// busyLines are the dialog's lines while the Account is being saved.
func (f *addAccountForm) busyLines() []string {
	if f.editing != nil {
		return []string{"", hintStyle.Render("saving… Esc cancels")}
	}

	return []string{"", hintStyle.Render("adding… Esc cancels")}
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

	fields := make([]dialogField, 0, 2+len(f.creds))
	fields = append(fields,
		dialogField{label: "Provider", value: provider, choice: f.editing == nil, fixed: f.editing != nil},
		dialogField{label: "Name", value: name},
	)

	for i, fd := range f.creds {
		fields = append(fields, dialogField{label: fd.Label, value: values[i], masked: fd.Secret, choice: picked(fd)})
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
		if f.editing == nil && f.tenantField() != "" {
			hint += " Left empty, the Tenant's name is suggested once the credentials are checked."
		}
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

// credentialHint is the hint of a credential field, which names the
// default an empty field takes.
func credentialHint(fd dnsprovider.Field, editing bool) string {
	hint := credentialPurpose(fd, editing)
	if optional(fd) && !editing {
		hint += " Left empty, it is " + fd.Default + "."
	}

	return hint
}

// credentialPurpose says what a credential field is for.
func credentialPurpose(fd dnsprovider.Field, editing bool) string {
	switch {
	case editing && fd.Secret:
		return "Type a new " + fd.Label + ", or leave it empty to keep the current one."
	case editing:
		return "The " + fd.Label + " the Provider gave the Account; left empty, it keeps the current one."
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
		footer := m.accountFormFooter(field, field == f.saveField())
		size = size.grow(sizedDialogView(f.title(), fields, field, true, nil, footer, dialogSize{}))
	}

	return size
}
