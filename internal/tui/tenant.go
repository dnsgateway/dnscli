package tui

import (
	"cmp"
	"context"
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// tenantPick asks which Tenant an Account acts within, when its stored
// credentials name none and reach several, as those of an Account an
// earlier dnscli stored. It opens on the Account's first use, which
// failed for want of one, and the picked Tenant is saved with the
// credentials before every use that asked for it runs again.
type tenantPick struct {
	// accountID is the Account's: it is looked up when the Tenant is
	// picked, as it may have been renamed meanwhile.
	accountID int64
	tenants   []dnsprovider.Tenant
	picker    *picker
	// retries run again the uses that asked for the Tenant.
	retries []func() tea.Cmd
	// picked is the Tenant picked; nil while none is.
	picked *dnsprovider.Tenant
}

// tenantUse is the result of a use of an Account's Provider, which fails
// with a *provider.TenantChoiceError while the Account has no Tenant and
// its credentials reach several.
type tenantUse interface {
	// tenantUse is the Account used, a func that runs the use again,
	// with the Account and Zone as they are by then, and the use's error.
	tenantUse(m *Model) (accountID int64, retry func() tea.Cmd, err error)
}

// askTenant opens the Tenant picker when msg reports a use that failed for
// want of a Tenant: every use of an Account's Provider is reported by a
// tenantUse, so each one asks here, but the Onboard dialog's, which asks
// in the dialog. A use failing so while the picker is
// open for its Account runs again with the others once the Tenant is
// saved. A picker open for another Account, or another dialog, is left
// alone: the next use asks again.
func (m *Model) askTenant(msg tea.Msg) {
	u, ok := msg.(tenantUse)
	if !ok {
		return
	}

	id, retry, err := u.tenantUse(m)

	var choice *provider.TenantChoiceError
	if !errors.As(err, &choice) {
		return
	}

	a, ok := m.account(id)
	if !ok {
		return
	}

	if tp := m.tenantPick; tp != nil {
		if tp.accountID == id {
			tp.retries = append(tp.retries, retry)
		}

		return
	}

	if m.dialogOpen() {
		return
	}

	tp := &tenantPick{accountID: id, tenants: choice.Tenants, retries: []func() tea.Cmd{retry}}
	tp.picker = &picker{title: "Tenant of " + a.Label(), options: tenantOptions(choice.Tenants), pick: func(i int) { tp.picked = &tp.tenants[i] }}
	m.tenantPick = tp
	m.notice = notice{text: "The credentials of " + a.Label() + " reach several Tenants: pick the one it acts within."}
}

// dialogOpen reports whether a dialog or prompt is open over the panes.
func (m *Model) dialogOpen() bool {
	return m.help || m.unseal != nil || m.addForm != nil || m.keychain != nil || m.remove != nil || m.zoneEdit != nil || m.onboard != nil || m.offboard != nil ||
		m.recordForm != nil || m.templateForm != nil || m.preview != nil
}

// tenantPickKey handles a key while the Tenant picker is open: Enter
// saves the Tenant picked, Esc leaves the Account without one. An edit
// dialog asked for while it was open opens once it closes.
func (m *Model) tenantPickKey(msg tea.KeyMsg) tea.Cmd {
	a := m.keys.action(msg.String())
	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	tp := m.tenantPick
	if !tp.picker.key(a) {
		return nil
	}

	m.tenantPick = nil
	edit := m.editHeldBack()

	account, ok := m.account(tp.accountID)

	switch {
	case !ok:
		m.notice = notice{text: "The Account was removed meanwhile."}

		return edit
	case tp.picked == nil:
		m.notice = notice{text: account.Label() + " acts within no Tenant yet: it is asked for on its next use."}

		return edit
	}

	m.notice = notice{text: "Saving the Tenant of " + account.Label() + "…"}

	return tea.Batch(m.pinTenant(account, tp), edit)
}

// editHeldBack opens the edit dialog held back while the Tenant picker
// was open, reading the Account and its credentials as they are now.
func (m *Model) editHeldBack() tea.Cmd {
	id := m.editAfterPick
	m.editAfterPick = 0

	a, ok := m.account(id)
	if id == 0 || !ok {
		return nil
	}

	return m.editAccountOf(a)
}

// tenantPinnedMsg reports that an Account's credentials were saved with
// the Tenant picked for them.
type tenantPinnedMsg struct {
	account dns.Account
	pick    *tenantPick
	err     error
}

// pinTenant saves the Tenant picked with the credentials of a, the
// Account as it is now, once verified within it, unless they were saved
// again meanwhile.
func (m *Model) pinTenant(a dns.Account, tp *tenantPick) tea.Cmd {
	ctx, s, key, providers := m.ctx, m.deps.Store, m.key, m.providers
	field := provider.TenantField(m.deps.Providers[a.Provider])

	return func() tea.Msg {
		return tenantPinnedMsg{account: a, pick: tp, err: saveTenant(ctx, s, key, providers, a, field, tp.picked.ID)}
	}
}

// saveTenant saves the Tenant of ID id, in field, with the credentials of
// a, the Account as it is now, once verified within it, unless they were
// saved again meanwhile.
func saveTenant(ctx context.Context, s *store.Store, key *vault.Key, providers *provider.Opener, a dns.Account, field, id string) error {
	sealed, fields, err := sealedCredentials(ctx, s, key, providers, a)
	if err == nil && fields == nil {
		err = errors.New("this Account has no credentials")
	}

	if err != nil {
		return err
	}

	fields[field] = id

	if fields, _, err = verifyCredentials(ctx, providers, a.Provider, fields); err != nil {
		return err
	}

	replaced, err := replaceCredentials(ctx, s, key, a, sealed, fields)
	if err == nil && !replaced {
		err = errors.New("its credentials were saved meanwhile")
	}

	return err
}

// tenantPinned reports a saved Tenant, and runs again every use that
// asked for it.
func (m *Model) tenantPinned(msg tenantPinnedMsg) tea.Cmd {
	label := msg.account.Label()

	if msg.err != nil {
		m.notice = notice{text: "Could not save the Tenant of " + label + ": " + msg.err.Error(), err: true}

		return nil
	}

	m.notice = notice{text: label + " acts within " + cmp.Or(msg.pick.picked.Name, msg.pick.picked.ID) + "."}

	cmds := make([]tea.Cmd, len(msg.pick.retries))
	for i, retry := range msg.pick.retries {
		cmds[i] = retry()
	}

	return tea.Batch(cmds...)
}

// tenantOptions are the Tenants as a picker lists them: by name, else
// by ID.
func tenantOptions(tenants []dnsprovider.Tenant) []string {
	names := make([]string, len(tenants))
	for i, t := range tenants {
		names[i] = cmp.Or(t.Name, t.ID)
	}

	return names
}
