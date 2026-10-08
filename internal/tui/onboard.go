package tui

import (
	"errors"
	"strings"

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
	// done is the Zone Onboarded, as cached; nil until then.
	done *dns.Zone
	err  string
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
	// replaced is the ID of the cached Zone of that name the new one
	// replaced, such as one Moved; zero when none.
	replaced int64
	// err is why the Provider failed the Onboarding.
	err error
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
		m.notice = notice{text: "Show one Account's Zones first (" + m.keys.keysFor(ActionSelect) +
			" on it in the Accounts pane): a zone name is Onboarded in that Account.", err: true}

		return nil
	}

	// Whether the Provider can is known only once it is opened, which
	// needs secrets; a Provider type that cannot is refused before asking
	// for them.
	if !onboardType(a.Provider) {
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

// onboardType reports whether Provider type typ may Onboard a zone name.
// The module says so only of a Provider built with credentials
// (dnsprovider.CanCreateZone), not of its Factory: dnscli knows the types
// that do, Cloudflare's alone, and still checks the Provider once open.
func onboardType(typ string) bool {
	return typ == "cloudflare"
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
// again Onboards it; Esc goes back from confirming, else closes.
func (m *Model) onboardKey(msg tea.KeyMsg) tea.Cmd {
	d := m.onboard
	a := m.keys.action(msg.String())

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if d.busy {
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
// returns it as Providers list it.
func (m *Model) checkOnboardName(a dns.Account, typed string) (string, error) {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(typed), ".")), nil
}

// createZone Onboards the zone name confirmed, and caches the Zone the
// Provider returns, in place of a cached one of that name it replaces.
func (m *Model) createZone(d *onboardDialog) tea.Cmd {
	ctx, s, p, a, name := m.ctx, m.deps.Store, d.p, d.account, d.name

	var replaced int64

	for _, z := range m.zones {
		if z.AccountID == a.ID && nameKey(z.Name) == nameKey(name) {
			replaced = z.ID
		}
	}

	return func() tea.Msg {
		mz, err := p.CreateZone(ctx, name)
		if err != nil {
			return zoneOnboardedMsg{account: a, name: name, err: err}
		}

		z := provider.CacheZone(mz)
		z.AccountID = a.ID

		saved, err := s.PutZone(ctx, z)
		if err != nil {
			return zoneOnboardedMsg{account: a, name: name, zone: z, saveErr: err}
		}

		return zoneOnboardedMsg{account: a, name: name, zone: saved, cached: true, replaced: replaced}
	}
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
			d.busy, d.confirming = false, false
			d.err = "Could not Onboard " + msg.name + ": " + msg.err.Error()
		}

		return nil
	case msg.saveErr != nil:
		if d != nil {
			m.onboard = nil
		}

		m.notice = notice{text: msg.name + " was Onboarded at " + msg.account.Label() + ", but could not be cached: " +
			msg.saveErr.Error() + ". Re-list the Account's Zones (" + m.keys.keysFor(ActionRelistZones) + " in the Accounts pane).", err: true}

		return nil
	}

	if msg.replaced != 0 && msg.replaced == msg.zone.ID {
		// The Zone cached under that name is another zone at the Provider:
		// nothing this session built on it holds.
		m.forgetZones([]int64{msg.replaced})
	}

	if d != nil {
		z := msg.zone
		d.busy, d.done = false, &z
	}

	m.notice = notice{text: msg.zone.Name + " is Onboarded at " + msg.account.Label() + ", pending until Delegation names its nameservers."}

	return m.loadCache
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

		if len(z.Nameservers) == 0 {
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
		hint = "onboarding…"
	case d.confirming:
		hint = m.keys.keysFor(ActionSelect) + " confirm · " + m.keys.keysFor(ActionClose) + " back"
	default:
		hint = m.keys.keysFor(ActionSelect) + " next · " + m.keys.keysFor(ActionClose) + " close"
	}

	notes = append(notes, "", hintStyle.Render(hint))

	return dialogView(title, fields, 0, !d.confirming && !d.busy, notes)
}

// hostingWord says a Hosting State: "active", "pending", "moved" or
// "unknown".
func hostingWord(s dnsprovider.HostingState) string {
	switch s {
	case dnsprovider.HostingActive:
		return "active"
	case dnsprovider.HostingPending:
		return "pending"
	case dnsprovider.HostingMoved:
		return "moved"
	}

	return "unknown"
}
