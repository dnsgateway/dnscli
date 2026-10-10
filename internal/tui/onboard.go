package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/store"
)

// The Onboard dialog's fields, in order. The Tenant is shown only for a
// Provider type that takes one.
const (
	onboardFieldAccount = iota
	onboardFieldTenant
	onboardFieldName
)

// onboardDialog is the dialog that Onboards a zone name in an Account, a
// Zone write: the user chooses the Account, sees the Tenant it acts
// within, or picks one, types the name, confirms that it starts DNS
// hosting and leaves the domain's registration alone, and the new Zone,
// pending, is shown with the nameservers its Delegation must name.
type onboardDialog struct {
	// accounts are the Accounts whose Provider type can Onboard, as the
	// Accounts pane lists them; account indexes the one chosen.
	accounts []dns.Account
	account  int
	// field is the focused field, an onboardField.
	field int
	// picker lists the Accounts, or the Tenants to pick from; nil when
	// closed.
	picker *picker
	// picked is what the item picked starts, run once the picker closes.
	picked tea.Cmd
	// opening is set while the chosen Account's Provider is opened.
	// attempt counts the openings: only the last one's answer is taken,
	// and stop cancels it, and the naming of its Tenant.
	opening bool
	attempt int
	stop    context.CancelFunc
	// p is the chosen Account's Provider, a dnsprovider.ZoneCreator; nil
	// until it is opened, or when it cannot Onboard.
	p dnsprovider.ZoneCreator
	// tenant names the Tenant the Account acts within; empty for a
	// Provider type that takes none.
	tenant string
	// tenants are the Tenants to choose from, while the Account acts
	// within none and its credentials reach several; tenantAt indexes the
	// one chosen, which is saved with them once the Onboarding is
	// confirmed.
	tenants  []dnsprovider.Tenant
	tenantAt int
	// refused says why the chosen Account cannot Onboard: its Provider
	// could not be opened, cannot, or acts within no Tenant.
	refused string
	name    string
	caret   int
	// confirming is set once the name is checked, until it is confirmed
	// or Esc goes back to it.
	confirming bool
	busy       bool // the zone name is being Onboarded
	// cancel stops the create in flight; nil when none is.
	cancel context.CancelFunc
	// done is the Zone Onboarded, as cached; nil until then.
	done *dns.Zone
	// unsaid is set when done is the zone name alone, uncached: the
	// Provider did not say its ID or nameservers.
	unsaid bool
	err    string
}

// chosen is the Account the zone name is Onboarded in.
func (d *onboardDialog) chosen() dns.Account {
	return d.accounts[d.account]
}

// tenantLabel names the Tenant the zone name is Onboarded within: the
// one chosen, else the one the Account acts within.
func (d *onboardDialog) tenantLabel() string {
	if len(d.tenants) > 0 {
		return tenantOptions(d.tenants)[d.tenantAt]
	}

	return d.tenant
}

// stopOpening cancels opening the chosen Account's Provider, and naming
// its Tenant.
func (d *onboardDialog) stopOpening() {
	if d.stop != nil {
		d.stop()
		d.stop = nil
	}

	d.opening = false
}

// closeOnboard closes the Onboard dialog.
func (m *Model) closeOnboard() {
	m.onboard.stopOpening()
	m.onboard = nil
}

// onboardOpenedMsg reports the Provider opened for the Onboard dialog;
// p is nil when it failed. fields are the credentials it was built from,
// and ctx the opening's, which names their Tenant next.
type onboardOpenedMsg struct {
	ctx     context.Context //nolint:containedctx // the opening's, canceled with it
	account dns.Account
	attempt int
	p       dnsprovider.Provider
	fields  map[string]string
	err     error
}

// onboardTenantNamedMsg reports the name of the Tenant the Account opened
// by an attempt acts within.
type onboardTenantNamedMsg struct {
	attempt int
	name    string
}

// zoneOnboardedMsg reports an Onboarding: the Zone as cached when cached
// is set, else as the Provider returned it.
type zoneOnboardedMsg struct {
	account dns.Account
	name    string
	zone    dns.Zone
	cached  bool
	// replaced is set when the new Zone replaced a cached Zone of that
	// name, such as one Moved.
	replaced bool
	// unsaid is set when the Provider took the create without saying the
	// new Zone's ID or nameservers. Such a Zone is not cached: the cache
	// would hold a Zone nothing can address at the Provider, shown and
	// acted on as any other, until a re-list, which the user is told to
	// run, lists it whole.
	unsaid bool
	// err is why the Provider failed the Onboarding.
	err error
	// canceled is set when the user canceled it.
	canceled bool
	// readBack is why reading back a create whose answer was lost, or
	// that was refused as held already, failed.
	readBack error
	// saveErr is why the new Zone could not be cached, after the
	// Provider Onboarded it.
	saveErr error
	// tenant is the Tenant chosen in the dialog, saved with the Account's
	// credentials before the create; nil when none was. saved is set once
	// it is, and p is then the Provider opened within it.
	tenant *dnsprovider.Tenant
	saved  bool
	p      dnsprovider.ZoneCreator
	// tenantErr is why the Tenant could not be saved, or the Account
	// opened within it: nothing was Onboarded.
	tenantErr error
}

// openOnboard opens the Onboard dialog, with the Account the Domains pane
// shows the Zones of chosen, else the one highlighted in the Accounts
// pane, when their Provider types can Onboard. Onboarding is a Zone
// write: a sealed session Unseals first.
func (m *Model) openOnboard() tea.Cmd {
	if accounts, _ := m.onboardAccounts(); len(accounts) == 0 {
		m.notice = notice{text: "No Account can Onboard a zone: their Providers host only the domains they register.", err: true}

		return nil
	}

	return m.withSecrets(func() tea.Cmd {
		accounts, i := m.onboardAccounts()
		if len(accounts) == 0 || m.dialogOpen() || m.tenantPick != nil {
			return nil
		}

		m.onboard = &onboardDialog{accounts: accounts, account: i, field: onboardFieldName}

		return m.openOnboardProvider()
	})
}

// onboardAccounts are the Accounts whose Provider type can Onboard, and
// the index of the one to choose first: the one the Domains pane shows
// the Zones of, else the one highlighted in the Accounts pane, else the
// first.
func (m *Model) onboardAccounts() ([]dns.Account, int) {
	var accounts []dns.Account

	for _, a := range m.accounts {
		if m.accountCan(a, dnsprovider.CapZoneCreator) {
			accounts = append(accounts, a)
		}
	}

	prefer := m.accountFilter
	if prefer == 0 && m.cursor[paneAccounts] < len(m.accounts) {
		prefer = m.accounts[m.cursor[paneAccounts]].ID
	}

	for i, a := range accounts {
		if a.ID == prefer {
			return accounts, i
		}
	}

	return accounts, 0
}

// accountCan reports whether the Factory of an Account's Provider type
// declares the Zone write c, which is known before the Provider is
// opened; the Provider, once open, is checked still.
func (m *Model) accountCan(a dns.Account, c dnsprovider.Capability) bool {
	return m.deps.Providers[a.Provider].Can(c)
}

// cannotOnboard is the notice for an Account whose Provider cannot
// Onboard a zone name.
func cannotOnboard(a dns.Account) string {
	return a.Label() + " cannot Onboard a zone: its Provider hosts only the domains it registers."
}

// chooseOnboardAccount makes the i-th Account the one Onboarded in, and
// opens its Provider.
func (m *Model) chooseOnboardAccount(i int) tea.Cmd {
	d := m.onboard
	if i == d.account && (d.p != nil || d.opening) {
		return nil
	}

	d.account = i

	return m.openOnboardProvider()
}

// openOnboardProvider opens the Provider of the chosen Account, as it is
// now, canceling an opening in flight. A Tenant the Account acts within
// is asked for in the dialog, not the Tenant picker.
func (m *Model) openOnboardProvider() tea.Cmd {
	d := m.onboard
	d.stopOpening()
	d.attempt++
	d.p, d.tenant, d.tenants, d.tenantAt, d.refused, d.err = nil, "", nil, 0, "", ""

	a, ok := m.account(d.chosen().ID)
	if !ok {
		d.refused = "The Account was removed meanwhile."

		return nil
	}

	d.accounts[d.account] = a
	d.opening = true

	ctx, stop := context.WithCancel(m.ctx)
	d.stop = stop
	s, key, providers, attempt := m.deps.Store, m.key, m.providers, d.attempt

	return func() tea.Msg {
		p, fields, err := accountProvider(ctx, s, key, providers, a)

		return onboardOpenedMsg{ctx: ctx, account: a, attempt: attempt, p: p, fields: fields, err: err}
	}
}

// tenantNameTimeout bounds naming the Tenant, which the dialog does not
// wait for.
const tenantNameTimeout = 10 * time.Second

// nameTenant names the Tenant of ID id that p acts within, as its Account
// says; the dialog shows its ID until then.
func nameTenant(ctx context.Context, attempt int, p dnsprovider.Provider, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, tenantNameTimeout)
		defer cancel()

		info, err := p.Account(ctx)
		if err != nil || info.Tenant.Name == "" || (info.Tenant.ID != "" && info.Tenant.ID != id) {
			return nil
		}

		return onboardTenantNamedMsg{attempt: attempt, name: info.Tenant.Name}
	}
}

// onboardTenantNamed names the Tenant the chosen Account acts within.
func (m *Model) onboardTenantNamed(msg onboardTenantNamedMsg) {
	if d := m.onboard; d != nil && msg.attempt == d.attempt && d.p != nil {
		d.tenant = msg.name
	}
}

// onboardOpened takes the chosen Account's Provider, unless it could not
// be opened, cannot Onboard, or acts within no Tenant, which the dialog
// says. Credentials reaching several Tenants have one chosen in the
// dialog. The Tenant the Account acts within is named next.
func (m *Model) onboardOpened(msg onboardOpenedMsg) tea.Cmd {
	d := m.onboard
	if d == nil || !d.opening || msg.attempt != d.attempt {
		return nil
	}

	d.opening = false

	var choice *provider.TenantChoiceError

	switch {
	case errors.As(msg.err, &choice):
		d.tenants = choice.Tenants
	case msg.err != nil:
		d.refused = "Could not open " + msg.account.Label() + ": " + msg.err.Error()
	case !dnsprovider.CanCreateZone(msg.p):
		d.refused = cannotOnboard(msg.account)
	case m.unscoped(msg.account, msg.fields):
		d.refused = noTenantToOnboard(msg.account)
	default:
		d.p = msg.p.(dnsprovider.ZoneCreator)

		if field := provider.TenantField(m.deps.Providers[msg.account.Provider]); field != "" {
			d.tenant = msg.fields[field]

			return nameTenant(msg.ctx, msg.attempt, msg.p, d.tenant)
		}
	}

	return nil
}

// unscoped reports whether an Account's Provider, built from fields, acts
// within no Tenant although its type takes one: its credentials reach
// none, or may not list them.
func (m *Model) unscoped(a dns.Account, fields map[string]string) bool {
	f, ok := m.deps.Providers[a.Provider]
	if !ok {
		return false
	}

	field := provider.TenantField(f)

	return field != "" && fields[field] == ""
}

// noTenantToOnboard is the notice for an Account acting within no
// Tenant, which has nowhere to Onboard a zone name.
func noTenantToOnboard(a dns.Account) string {
	return a.Label() + " acts within no single Provider account (Tenant), which Onboarding needs: " +
		"its credentials may not list the accounts they reach. Use credentials that may."
}

// onboardFields are the dialog's fields, in order: the Tenant only for a
// Provider type that takes one.
func (m *Model) onboardFields(d *onboardDialog) []int {
	if provider.TenantField(m.deps.Providers[d.chosen().Provider]) == "" {
		return []int{onboardFieldAccount, onboardFieldName}
	}

	return []int{onboardFieldAccount, onboardFieldTenant, onboardFieldName}
}

// onboardKey handles a key while the Onboard dialog is open: Up and Down
// move through the fields, Left and Right change the Account or the
// Tenant to choose, Enter lists the Accounts or the Tenants, checks the name typed and asks to
// confirm, and Enter again Onboards it; Esc goes back from confirming,
// else closes, and cancels a create in flight, which is then read back as
// one whose answer was lost.
func (m *Model) onboardKey(msg tea.KeyMsg) tea.Cmd {
	d := m.onboard
	a := m.keys.action(msg.String())

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if d.busy {
		if a == ActionClose && d.cancel != nil {
			d.cancel()
		}

		return nil
	}

	if d.done != nil {
		if a == ActionClose || a == ActionSelect {
			m.closeOnboard()
		}

		return nil
	}

	if d.picker != nil {
		if d.picker.key(a) {
			d.picker = nil
		}

		cmd := d.picked
		d.picked = nil

		return cmd
	}

	step := 0

	switch a {
	case ActionPaneRight:
		step = 1
	case ActionPaneLeft:
		step = -1
	}

	if d.confirming {
		switch a {
		case ActionClose:
			d.confirming = false
		case ActionSelect:
			d.busy = true

			return m.createZone(d)
		}

		return nil
	}

	if d.field == onboardFieldName && m.keys.typeInto(&d.name, &d.caret, msg) {
		d.err = ""

		return nil
	}

	fields := m.onboardFields(d)
	at := max(slices.Index(fields, d.field), 0)

	switch a {
	case ActionClose:
		m.closeOnboard()
	case ActionNextPane, ActionDown:
		d.field = fields[(at+1)%len(fields)]
	case ActionPrevPane, ActionUp:
		d.field = fields[(at+len(fields)-1)%len(fields)]
	case ActionPaneLeft, ActionPaneRight:
		switch {
		case d.field == onboardFieldAccount && len(d.accounts) > 1:
			return m.chooseOnboardAccount((d.account + step + len(d.accounts)) % len(d.accounts))
		case d.choosesTenant():
			d.tenantAt = (d.tenantAt + step + len(d.tenants)) % len(d.tenants)
		}
	case ActionSelect:
		return m.onboardSelect()
	}

	return nil
}

// choosesTenant reports whether the Tenant field chooses one of the
// Tenants the Account's credentials reach.
func (d *onboardDialog) choosesTenant() bool {
	return d.field == onboardFieldTenant && len(d.tenants) > 0
}

// onboardSelect handles Enter on the focused field: it lists the
// Accounts, or the Tenants to choose from, and checks the name typed.
func (m *Model) onboardSelect() tea.Cmd {
	d := m.onboard

	switch {
	case d.field == onboardFieldAccount && len(d.accounts) > 1:
		labels := make([]string, len(d.accounts))
		for i, a := range d.accounts {
			labels[i] = a.Label()
		}

		d.picker = &picker{title: "Account", options: labels, cursor: d.account, pick: func(i int) { d.picked = m.chooseOnboardAccount(i) }}
	case d.choosesTenant():
		d.picker = &picker{
			title: "Tenant of " + d.chosen().Label(), options: tenantOptions(d.tenants), cursor: d.tenantAt,
			pick: func(i int) { d.tenantAt = i },
		}
	case d.field != onboardFieldName:
		d.field = onboardFieldName
	default:
		m.confirmOnboard()
	}

	return nil
}

// confirmOnboard checks the name typed and asks to confirm Onboarding it
// in the chosen Account, once its Provider is open and can, within the
// Tenant chosen, if any. An Account that cannot is refused as the dialog
// says already.
func (m *Model) confirmOnboard() {
	d := m.onboard

	switch {
	case d.refused != "":
		return
	case d.p == nil && len(d.tenants) == 0:
		d.err = "Wait for " + d.chosen().Label() + " to open."

		return
	}

	name, err := m.checkOnboardName(d.chosen(), d.name)
	if err != nil {
		d.err = err.Error()

		return
	}

	d.name, d.caret, d.confirming, d.err = name, 0, true, ""
}

// checkOnboardName checks a zone name typed to Onboard in an Account, and
// returns it as Providers list it: lower case, without a trailing dot. It
// must be a domain name, in ASCII, of two labels at least, and not one
// the Account already hosts; a Moved Zone's name may be Onboarded again.
func (m *Model) checkOnboardName(a dns.Account, typed string) (string, error) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(typed), "."))
	if err := checkZoneName(name); err != nil {
		return "", err
	}

	for _, z := range m.zones {
		if z.AccountID == a.ID && nameKey(z.Name) == name && z.State != dnsprovider.HostingMoved {
			return "", fmt.Errorf("%s has %s already.", a.Label(), name)
		}
	}

	return name, nil
}

// checkZoneName checks that name, in lower case without a trailing dot,
// is a domain name a zone can be Onboarded for: in ASCII, of two labels
// at least, and a host name as the module checks one. The Provider
// refuses a name it cannot host, such as one no registry holds.
func checkZoneName(name string) error {
	switch {
	case name == "":
		return errors.New("Enter a zone name, such as example.com.")
	case strings.ContainsFunc(name, func(r rune) bool { return r > unicode.MaxASCII }):
		return errors.New("Enter the zone name in ASCII: an internationalized name in its punycode form (xn--…).")
	case !strings.Contains(name, "."):
		return errors.New("Enter a domain such as example.com, not a top-level domain alone.")
	case dnsprovider.CheckValue("CNAME", name) != nil:
		return errors.New("A zone name is labels of letters, digits and hyphens, joined by dots, " +
			"each label at most 63 characters, the name at most 253.")
	}

	return nil
}

// onboardTimeout bounds a create, and, apart, its read-back: the create
// is sent once, never retried.
const onboardTimeout = time.Minute

// createZone Onboards the zone name confirmed, and caches the Zone the
// Provider returns, in place of a cached one of that name it replaces. A
// create whose answer was lost, or that the Provider refuses as held
// already, is read back (see readBack). A Zone the Provider returns
// without its ID is not cached: see zoneOnboardedMsg.unsaid. A Tenant
// chosen in the dialog is saved with the Account's credentials first, and
// the create sent within it.
func (m *Model) createZone(d *onboardDialog) tea.Cmd {
	parent, s, p, a, name := m.ctx, m.deps.Store, d.p, d.chosen(), d.name
	key, providers := m.key, m.providers

	var tenant *dnsprovider.Tenant
	if len(d.tenants) > 0 {
		tenant = &d.tenants[d.tenantAt]
	}

	field := provider.TenantField(m.deps.Providers[a.Provider])

	ctx, cancel := context.WithTimeout(parent, onboardTimeout)
	d.cancel = cancel

	return func() tea.Msg {
		defer cancel()

		var within zoneOnboardedMsg

		if tenant != nil {
			within = zoneOnboardedMsg{account: a, name: name, tenant: tenant}

			if within.tenantErr = saveTenant(ctx, s, key, providers, a, field, tenant.ID); within.tenantErr != nil {
				return within
			}

			within.saved = true

			opened, fields, err := accountProvider(ctx, s, key, providers, a)

			switch {
			case err != nil:
				within.tenantErr = err
			case !dnsprovider.CanCreateZone(opened):
				within.tenantErr = errors.New("its Provider cannot Onboard a zone")
			case fields[field] != tenant.ID:
				within.tenantErr = errors.New("its credentials were saved meanwhile")
			}

			if within.tenantErr != nil {
				return within
			}

			p = opened.(dnsprovider.ZoneCreator)
			within.p = p
		}

		msg := onboard(ctx, parent, s, p, a, name)
		msg.tenant, msg.saved, msg.p = within.tenant, within.saved, within.p

		return msg
	}
}

// onboard Onboards name in Account a through p, its Provider, and caches
// the Zone it returns: see createZone.
func onboard(ctx, parent context.Context, s *store.Store, p dnsprovider.ZoneCreator, a dns.Account, name string) zoneOnboardedMsg {
	mz, err := p.CreateZone(ctx, name)
	if err != nil {
		if !answerLost(err) {
			return zoneOnboardedMsg{account: a, name: name, err: err}
		}

		held, rerr := readBack(parent, p, name)
		if rerr != nil || held.ID == "" {
			return zoneOnboardedMsg{
				account: a, name: name, err: err, canceled: errors.Is(err, context.Canceled),
				readBack: rerr,
			}
		}

		mz = held
	}

	z := provider.CacheZone(mz)
	z.AccountID = a.ID

	if z.ProviderID == "" {
		return zoneOnboardedMsg{account: a, name: name, zone: z, unsaid: true}
	}

	saved, replaced, err := s.PutZone(parent, z)
	if err != nil {
		return zoneOnboardedMsg{account: a, name: name, zone: z, saveErr: err}
	}

	return zoneOnboardedMsg{account: a, name: name, zone: saved, cached: true, replaced: replaced}
}

// answerLost reports whether a failed create may have been taken, or the
// Provider says the name is held already, so the Account may hold it:
// a conflict, or a failure without the Provider's answer, such as a lost
// connection, a timeout or a cancel. A Provider without a Tenant asked
// nothing.
func answerLost(err error) bool {
	if errors.Is(err, dnsprovider.ErrConflict) {
		return true
	}

	return !errors.As(err, new(*dnsprovider.APIError)) && !errors.Is(err, dnsprovider.ErrNoTenant)
}

// readBack reads the Zone of a name within the Provider's Tenant after a
// create whose answer was lost or that was refused as held already: the
// Zone the Account holds, Pending or Active, which it Onboarded, else
// none (no ID). err is why the read failed.
func readBack(parent context.Context, p dnsprovider.ZoneCreator, name string) (dnsprovider.Zone, error) {
	ctx, cancel := context.WithTimeout(parent, onboardTimeout)
	defer cancel()

	z, err := p.(dnsprovider.Provider).Zone(ctx, name)

	switch {
	case errors.Is(err, dnsprovider.ErrNotFound):
		return dnsprovider.Zone{}, nil
	case err != nil:
		return dnsprovider.Zone{}, err
	case z.State != dnsprovider.HostingPending && z.State != dnsprovider.HostingActive:
		return dnsprovider.Zone{}, nil
	}

	return z, nil
}

// zoneOnboarded reports an Onboarding. One the Provider failed leaves the
// dialog open with the error and the cache as it was; one made shows the
// new Zone, pending, with its nameservers, and lists it in the Domains
// pane. One that could not be cached closes the dialog.
func (m *Model) zoneOnboarded(msg zoneOnboardedMsg) tea.Cmd {
	d := m.onboard
	if d != nil && d.chosen().ID != msg.account.ID {
		d = nil
	}

	if d != nil && msg.p != nil {
		// The Account acts within the Tenant chosen now.
		d.p, d.tenants, d.tenantAt, d.tenant = msg.p, nil, 0, cmp.Or(msg.tenant.Name, msg.tenant.ID)
	}

	switch {
	case msg.tenantErr != nil:
		return m.onboardTenantFailed(d, msg)
	case msg.err != nil:
		if d != nil {
			d.busy, d.confirming, d.cancel = false, false, nil
			d.err = m.onboardError(msg)
		}

		return nil
	case msg.saveErr != nil:
		if d != nil {
			m.closeOnboard()
		}

		m.notice = notice{text: msg.name + " was Onboarded at " + msg.account.Label() + ", but could not be cached: " +
			msg.saveErr.Error() + ". " + m.reList(), err: true}

		return nil
	}

	if msg.replaced {
		// The Zone cached under that name is another zone at the Provider:
		// nothing this session built on it holds.
		m.forgetZones([]int64{msg.zone.ID})
	}

	if d != nil {
		z := msg.zone
		d.busy, d.done, d.unsaid, d.cancel = false, &z, msg.unsaid, nil
	}

	m.notice = notice{text: msg.zone.Name + " is Onboarded at " + msg.account.Label() + ", pending until Delegation names its nameservers."}
	if msg.unsaid {
		m.notice = notice{text: msg.zone.Name + " is Onboarded at " + msg.account.Label() + ". " + m.reList()}

		return nil
	}

	return m.loadCache
}

// onboardTenantFailed reports an Onboarding that failed before the
// create, for want of the Tenant chosen. One saved already is the
// Account's: the dialog opens it again within it.
func (m *Model) onboardTenantFailed(d *onboardDialog, msg zoneOnboardedMsg) tea.Cmd {
	tenant := cmp.Or(msg.tenant.Name, msg.tenant.ID)
	text := "Could not save " + tenant + " as the Tenant of " + msg.account.Label() + ": " + msg.tenantErr.Error() + ". Nothing was Onboarded."

	if msg.saved {
		text = msg.account.Label() + " acts within " + tenant + " now, but could not be opened within it: " +
			msg.tenantErr.Error() + ". Nothing was Onboarded."
	}

	if d == nil {
		m.notice = notice{text: text, err: true}

		return nil
	}

	d.busy, d.confirming, d.cancel = false, false, nil

	var cmd tea.Cmd
	if msg.saved {
		cmd = m.openOnboardProvider()
	}

	d.err = text

	return cmd
}

// onboardError says why an Onboarding failed. One whose answer was lost,
// or refused as held already, says what the read-back found: that the
// Account holds no Zone of the name, or, when the read failed, that it
// may hold it all the same.
func (m *Model) onboardError(msg zoneOnboardedMsg) string {
	text := "Could not Onboard " + msg.name + ": " + strings.TrimSuffix(msg.err.Error(), ".") + "."
	if msg.canceled {
		text = "Canceled Onboarding " + msg.name + "."
	}

	if !answerLost(msg.err) {
		return text
	}

	if msg.readBack != nil {
		return text + " It may have been Onboarded all the same, as reading it back failed: " + msg.readBack.Error() + ". " + m.reList()
	}

	text += " " + msg.account.Label() + " holds no Zone of that name"
	if errors.Is(msg.err, dnsprovider.ErrConflict) {
		return text + ": another account holds it."
	}

	return text + ", so nothing was Onboarded."
}

// reList tells the user to re-list the Account's Zones.
func (m *Model) reList() string {
	return "Re-list the Account's Zones (" + m.keys.keysFor(ActionRelistZones) + " in the Accounts pane)."
}

func (m *Model) onboardView() string {
	d := m.onboard
	a := d.chosen()
	title := "Onboard a zone"

	if d.done != nil {
		z := d.done
		notes := wrapNote(lipgloss.NewStyle(), z.Name+" is Onboarded at "+a.Label()+", "+hostingWord(z.State)+
			": it is served from there only once its Delegation names these nameservers, at the registrar.", noteWidth)
		notes = append(notes, "")

		for _, ns := range z.Nameservers {
			notes = append(notes, "  "+ns)
		}

		switch {
		case d.unsaid:
			notes = append(notes, wrapNote(errorStyle, "The Provider took it but did not say its ID or nameservers, "+
				"so it is not cached yet. "+m.reList(), noteWidth)...)
		case len(z.Nameservers) == 0:
			notes = append(notes, wrapNote(errorStyle, "The Provider named no nameservers: see its console.", noteWidth)...)
		}

		notes = append(notes, "", hintStyle.Render(m.keys.keysFor(ActionClose)+" close"))

		return dialogView(title, nil, 0, false, notes)
	}

	fixed := d.confirming || d.busy
	order := m.onboardFields(d)
	fields := make([]dialogField, 0, len(order))

	for _, f := range order {
		switch f {
		case onboardFieldAccount:
			fields = append(fields, dialogField{label: "Account", value: a.Label(), choice: !fixed && len(d.accounts) > 1, fixed: fixed || len(d.accounts) == 1})
		case onboardFieldTenant:
			fields = append(fields, m.onboardTenantField(d, fixed))
		case onboardFieldName:
			fields = append(fields, dialogField{label: "Zone name", value: d.name, caret: d.caret, fixed: fixed})
		}
	}

	var notes []string

	switch {
	case d.confirming:
		at := a.Label()
		if t := d.tenantLabel(); t != "" {
			at += " (" + t + ")"
		}

		notes = append(notes, wrapNote(lipgloss.NewStyle(), "Onboard "+d.name+" at "+at+"?", noteWidth)...)
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, "This starts DNS hosting of "+d.name+" in "+a.Label()+
			". It does not register the domain, and leaves its registration untouched.", noteWidth)...)

		if len(d.tenants) > 0 {
			notes = append(notes, "")
			notes = append(notes, wrapNote(lipgloss.NewStyle(), d.tenantLabel()+" is saved as the Tenant "+a.Label()+
				" acts within, for every use of it.", noteWidth)...)
		}
	case d.refused != "":
		notes = append(notes, wrapNote(errorStyle, d.refused, noteWidth)...)
	case len(d.tenants) > 0:
		notes = append(notes, wrapNote(lipgloss.NewStyle(), "The credentials of "+a.Label()+
			" reach several Tenants: choose the one it acts within, which is saved with them.", noteWidth)...)
	default:
		notes = append(notes, wrapNote(hintStyle, "Starts DNS hosting of a domain you hold; it does not register it.", noteWidth)...)
	}

	if d.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, d.err, noteWidth)...)
	}

	k := m.keys.keysFor

	var hint string

	switch {
	case d.busy:
		hint = "onboarding… " + k(ActionClose) + " cancels"
	case d.confirming:
		hint = k(ActionSelect) + " confirm · " + k(ActionClose) + " back"
	case d.choosesTenant(), d.field == onboardFieldAccount && len(d.accounts) > 1:
		hint = k(ActionPaneLeft) + " " + k(ActionPaneRight) + " change · " + k(ActionSelect) + " list · " + k(ActionClose) + " close"
	default:
		hint = k(ActionUp) + " " + k(ActionDown) + " field · " + k(ActionSelect) + " next · " + k(ActionClose) + " close"
	}

	notes = append(notes, "", hintStyle.Render(hint))

	return dialogView(title, fields, max(slices.Index(order, d.field), 0), d.field == onboardFieldName && !fixed, notes)
}

// onboardTenantField shows the Tenant the chosen Account acts within, or
// that one is to be picked.
func (m *Model) onboardTenantField(d *onboardDialog, fixed bool) dialogField {
	f := dialogField{label: "Tenant", value: d.tenant, fixed: true}

	switch {
	case d.opening:
		f.value = "…"
	case len(d.tenants) > 0:
		f.value, f.choice, f.fixed = d.tenantLabel(), !fixed, fixed
	case d.tenant == "":
		f.value = "—"
	}

	return f
}
