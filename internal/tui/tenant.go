package tui

import (
	"cmp"
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// tenantPick asks which Tenant an Account acts within, when its stored
// credentials name none and reach several, as those of an Account an
// earlier dnscli stored. It opens on the Account's first use, which
// failed for want of one, and the picked Tenant is saved with the
// credentials before that use runs again.
type tenantPick struct {
	account dns.Account
	tenants []dnsprovider.Tenant
	picker  *picker
	// retry runs again the use that asked for the Tenant.
	retry func() tea.Cmd
	// picked is the Tenant picked; nil while none is.
	picked *dnsprovider.Tenant
}

// askTenant opens the Tenant picker for a's use that failed with err,
// when err asks for a Tenant, and retry runs that use again once one is
// saved. It reports whether it asked. A picker already open, or another
// dialog, is left alone: the next use asks again.
func (m *Model) askTenant(a dns.Account, err error, retry func() tea.Cmd) bool {
	var choice *provider.TenantChoiceError
	if !errors.As(err, &choice) {
		return false
	}

	if m.tenantPick != nil || m.dialogOpen() {
		return true
	}

	names := make([]string, len(choice.Tenants))
	for i, t := range choice.Tenants {
		names[i] = cmp.Or(t.Name, t.ID)
	}

	tp := &tenantPick{account: a, tenants: choice.Tenants, retry: retry}
	tp.picker = &picker{title: "Tenant of " + a.Label(), options: names, pick: func(i int) { tp.picked = &tp.tenants[i] }}
	m.tenantPick = tp
	m.notice = notice{text: "The credentials of " + a.Label() + " reach several Tenants: pick the one it acts within."}

	return true
}

// dialogOpen reports whether a dialog or prompt is open over the panes.
func (m *Model) dialogOpen() bool {
	return m.help || m.unseal != nil || m.addForm != nil || m.keychain != nil || m.remove != nil ||
		m.recordForm != nil || m.templateForm != nil || m.preview != nil || m.delegationOf != ""
}

// tenantPickKey handles a key while the Tenant picker is open: Enter
// saves the Tenant picked, Esc leaves the Account without one.
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

	if tp.picked == nil {
		m.notice = notice{text: tp.account.Label() + " acts within no Tenant yet: it is asked for on its next use."}

		return nil
	}

	m.notice = notice{text: "Saving the Tenant of " + tp.account.Label() + "…"}

	return m.pinTenant(tp)
}

// tenantPinnedMsg reports that an Account's credentials were saved with
// the Tenant picked for them.
type tenantPinnedMsg struct {
	pick *tenantPick
	err  error
}

// pinTenant saves the Tenant picked with the Account's credentials, once
// verified within it, unless they were saved again meanwhile.
func (m *Model) pinTenant(tp *tenantPick) tea.Cmd {
	ctx, s, key, providers := m.ctx, m.deps.Store, m.key, m.providers
	field := tenantFieldOf(m.deps.Providers[tp.account.Provider])

	return func() tea.Msg {
		sealed, fields, err := sealedCredentials(ctx, s, key, providers, tp.account)
		if err == nil && fields == nil {
			err = errors.New("this Account has no credentials")
		}

		if err != nil {
			return tenantPinnedMsg{pick: tp, err: err}
		}

		fields[field] = tp.picked.ID

		if fields, _, err = verifyCredentials(ctx, providers, tp.account.Provider, fields); err != nil {
			return tenantPinnedMsg{pick: tp, err: err}
		}

		replaced, err := replaceCredentials(ctx, s, key, tp.account, sealed, fields)
		if err == nil && !replaced {
			err = errors.New("its credentials were saved meanwhile")
		}

		return tenantPinnedMsg{pick: tp, err: err}
	}
}

// tenantPinned reports a saved Tenant, and runs again the use that asked
// for it.
func (m *Model) tenantPinned(msg tenantPinnedMsg) tea.Cmd {
	label := msg.pick.account.Label()

	if msg.err != nil {
		m.notice = notice{text: "Could not save the Tenant of " + label + ": " + msg.err.Error(), err: true}

		return nil
	}

	m.notice = notice{text: label + " acts within " + cmp.Or(msg.pick.picked.Name, msg.pick.picked.ID) + "."}

	return msg.pick.retry()
}

// tenantFieldOf is the name of the field f takes its Tenant in; empty
// when it takes none.
func tenantFieldOf(f dnsprovider.Factory) string {
	for _, fd := range f.Fields {
		if fd.Tenant {
			return fd.Name
		}
	}

	return ""
}
