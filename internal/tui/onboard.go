package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// onboardDialog is the dialog that Onboards a zone name in an Account, a
// Zone write: the user types the name, confirms that it starts DNS
// hosting and leaves the domain's registration alone, and the new Zone,
// pending, is shown with the nameservers its Delegation must name.
type onboardDialog struct {
	account dns.Account
	// p is the Account's Provider, a dnsprovider.ZoneCreator.
	p     dnsprovider.ZoneCreator
	name  string
	caret int
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

// onboardOpenedMsg reports the Provider opened for the Onboard dialog;
// p is nil when it failed. fields are the credentials it was built from.
type onboardOpenedMsg struct {
	account dns.Account
	p       dnsprovider.Provider
	fields  map[string]string
	err     error
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
	// cancelled is set when the user cancelled it.
	cancelled bool
	// readBack is why reading back a create whose answer was lost, or
	// that was refused as held already, failed.
	readBack error
	// saveErr is why the new Zone could not be cached, after the
	// Provider Onboarded it.
	saveErr error
}

// openOnboard opens the Onboard dialog for the selected Account: the one
// whose Zones the Domains pane shows, or the only one. Onboarding is a
// Zone write: a sealed session Unseals first, and the Account's Provider
// must be able to Onboard.
func (m *Model) openOnboard() tea.Cmd {
	a, ok := m.onboardAccount()
	if !ok {
		m.notice = notice{text: "Show one Account's Zones first (" + m.keys.keysFor(ActionShow) +
			" on it in the Accounts pane): a zone name is Onboarded in that Account.", err: true}

		return nil
	}

	// Whether the Provider can is known only once it is opened, which
	// needs secrets; a Provider type that cannot is refused before asking
	// for them.
	if !m.accountCan(a, dnsprovider.CapZoneCreator) {
		m.notice = notice{text: cannotOnboard(a), err: true}

		return nil
	}

	return m.withSecrets(func() tea.Cmd { return m.openOnboardOf(a.ID) })
}

// onboardAccount is the Account a zone name is Onboarded in: the one the
// Domains pane shows the Zones of, or the only one.
func (m *Model) onboardAccount() (dns.Account, bool) {
	if m.accountFilter != 0 {
		return m.account(m.accountFilter)
	}

	if len(m.accounts) == 1 {
		return m.accounts[0], true
	}

	return dns.Account{}, false
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

// openOnboardOf opens the Provider of an Account, as it is now, for the
// Onboard dialog.
func (m *Model) openOnboardOf(accountID int64) tea.Cmd {
	a, ok := m.account(accountID)
	if !ok {
		return nil
	}

	ctx, s, key, providers := m.ctx, m.deps.Store, m.key, m.providers

	return func() tea.Msg {
		p, fields, err := accountProvider(ctx, s, key, providers, a)

		return onboardOpenedMsg{account: a, p: p, fields: fields, err: err}
	}
}

// tenantUse opens the Onboard dialog again, for the Account as it is by
// then.
func (msg onboardOpenedMsg) tenantUse(m *Model) (int64, func() tea.Cmd, error) {
	return msg.account.ID, func() tea.Cmd { return m.openOnboardOf(msg.account.ID) }, msg.err
}

// onboardOpened opens the dialog, unless the Provider could not be
// opened, cannot Onboard, or has no Tenant to Onboard in, or another
// dialog opened meanwhile.
func (m *Model) onboardOpened(msg onboardOpenedMsg) {
	var choice *provider.TenantChoiceError

	switch {
	case errors.As(msg.err, &choice):
		return // the Tenant picker asks, and opens the dialog again
	case msg.err != nil:
		m.notice = notice{text: "Could not open " + msg.account.Label() + ": " + msg.err.Error(), err: true}

		return
	case !dnsprovider.CanCreateZone(msg.p):
		m.notice = notice{text: cannotOnboard(msg.account), err: true}

		return
	case m.unscoped(msg.account, msg.fields):
		m.notice = notice{text: noTenantToOnboard(msg.account), err: true}

		return
	case m.dialogOpen() || m.tenantPick != nil:
		return
	}

	m.onboard = &onboardDialog{account: msg.account, p: msg.p.(dnsprovider.ZoneCreator)}
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

// onboardKey handles a key while the Onboard dialog is open: the user
// types the zone name, Enter checks it and asks to confirm, and Enter
// again Onboards it; Esc goes back from confirming, else closes, and
// cancels a create in flight, which is then read back as one whose
// answer was lost.
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
			m.onboard = nil
		}

		return nil
	}

	if !d.confirming && m.keys.typeInto(&d.name, &d.caret, msg) {
		d.err = ""

		return nil
	}

	switch a {
	case ActionClose:
		if d.confirming {
			d.confirming = false
		} else {
			m.onboard = nil
		}
	case ActionSelect:
		if !d.confirming {
			name, err := m.checkOnboardName(d.account, d.name)
			if err != nil {
				d.err = err.Error()

				return nil
			}

			d.name, d.caret = name, 0
			d.confirming = true
			d.err = ""

			return nil
		}

		d.busy = true

		return m.createZone(d)
	}

	return nil
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
// without its ID is not cached: see zoneOnboardedMsg.unsaid.
func (m *Model) createZone(d *onboardDialog) tea.Cmd {
	parent, s, p, a, name := m.ctx, m.deps.Store, d.p, d.account, d.name

	ctx, cancel := context.WithTimeout(parent, onboardTimeout)
	d.cancel = cancel

	return func() tea.Msg {
		defer cancel()

		mz, err := p.CreateZone(ctx, name)
		if err != nil {
			if !answerLost(err) {
				return zoneOnboardedMsg{account: a, name: name, err: err}
			}

			held, rerr := readBack(parent, p, name)
			if rerr != nil || held.ID == "" {
				return zoneOnboardedMsg{account: a, name: name, err: err, cancelled: errors.Is(err, context.Canceled),
					readBack: rerr}
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
	if d != nil && d.account.ID != msg.account.ID {
		d = nil
	}

	switch {
	case msg.err != nil:
		if d != nil {
			d.busy, d.confirming, d.cancel = false, false, nil
			d.err = m.onboardError(msg)
		}

		return nil
	case msg.saveErr != nil:
		if d != nil {
			m.onboard = nil
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

// onboardError says why an Onboarding failed. One whose answer was lost,
// or refused as held already, says what the read-back found: that the
// Account holds no Zone of the name, or, when the read failed, that it
// may hold it all the same.
func (m *Model) onboardError(msg zoneOnboardedMsg) string {
	text := "Could not Onboard " + msg.name + ": " + strings.TrimSuffix(msg.err.Error(), ".") + "."
	if msg.cancelled {
		text = "Cancelled Onboarding " + msg.name + "."
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
	title := "Onboard a zone at " + d.account.Label()

	if d.done != nil {
		z := d.done
		notes := wrapNote(lipgloss.NewStyle(), z.Name+" is Onboarded at "+d.account.Label()+", "+hostingWord(z.State)+
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

	fields := []dialogField{{label: "Zone name", value: d.name, caret: d.caret, fixed: d.confirming}}

	var notes []string

	if d.confirming {
		notes = append(notes, wrapNote(lipgloss.NewStyle(), "Onboard "+d.name+" at "+d.account.Label()+"?", noteWidth)...)
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, "This starts DNS hosting of "+d.name+" in "+d.account.Label()+
			". It does not register the domain, and leaves its registration untouched.", noteWidth)...)
	} else {
		notes = append(notes, wrapNote(hintStyle, "Starts DNS hosting of a domain you hold; it does not register it.", noteWidth)...)
	}

	if d.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, d.err, noteWidth)...)
	}

	var hint string

	switch {
	case d.busy:
		hint = "onboarding… " + m.keys.keysFor(ActionClose) + " cancels"
	case d.confirming:
		hint = m.keys.keysFor(ActionSelect) + " confirm · " + m.keys.keysFor(ActionClose) + " back"
	default:
		hint = m.keys.keysFor(ActionSelect) + " next · " + m.keys.keysFor(ActionClose) + " close"
	}

	notes = append(notes, "", hintStyle.Render(hint))

	return dialogView(title, fields, 0, !d.confirming && !d.busy, notes)
}
