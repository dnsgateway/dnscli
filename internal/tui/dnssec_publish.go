package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// dsPublish publishes the open Zone's DS at its registrar: the Account,
// among those holding a Zone of the same name, whose Provider is a
// Registrar Adapter and registers the domain. The user confirms the
// Account and the keys first, as it is a Delegation write (ADR-0007).
type dsPublish struct {
	// zone is the Live Zone whose keys are published.
	zone dns.Zone
	keys []dns.SigningKey
	// registrar is the Registration the keys are published at, once
	// picked.
	registrar dns.Zone
	// picker lists the Registrations found to pick from, when several
	// were; nil once one is picked.
	picker *picker
	busy   bool
	err    string
}

// registrarsFoundMsg reports which Zones of a Live Zone's name are held
// by an Account that registers the domain and can publish its DS.
type registrarsFoundMsg struct {
	zoneID int64
	keys   []dns.SigningKey
	found  []dns.Zone
	// failed says why asking an Account failed, other than that it does
	// not register the domain.
	failed []string
}

// dsPublishedMsg reports the result of publishing a Zone's DS.
type dsPublishedMsg struct {
	zoneID int64
	err    error
}

// registrarCandidates are the Zones of z's name that may be its
// Registration, in the order they are tried: z itself, as one company
// often both hosts and registers a domain; then the Zones known to be
// registration only; then any other.
func (m *Model) registrarCandidates(z dns.Zone) []dns.Zone {
	var live, regOnly, other []dns.Zone

	for _, c := range m.zones {
		c = m.zoneByID[c.ID]

		switch {
		case nameKey(c.Name) != nameKey(z.Name):
		case c.ID == z.ID:
			live = append(live, c)
		case m.refreshOf(c.ID).notServed:
			regOnly = append(regOnly, c)
		default:
			other = append(other, c)
		}
	}

	return append(append(live, regOnly...), other...)
}

// registrarTimeout bounds asking one Account whether it registers a
// domain, and publishing its DS there.
const registrarTimeout = 30 * time.Second

// publishDS takes the step of publishing z's DS: it looks for the
// Registration of its name, to publish the keys there once the user
// confirms; without one, or while z may not be written, it shows the keys
// to copy into the registrar's panel.
func (m *Model) publishDS(z dns.Zone) tea.Cmd {
	keys := m.signingKeysOf(z)
	if m.writeRefusal(z) != "" || len(keys) == 0 {
		m.openSigningKeys()

		return nil
	}

	type candidate struct {
		zone    dns.Zone
		account dns.Account
	}

	var candidates []candidate

	for _, c := range m.registrarCandidates(z) {
		if a, ok := m.account(c.AccountID); ok {
			candidates = append(candidates, candidate{zone: c, account: a})
		}
	}

	m.notice = notice{text: "Looking for the registrar of " + z.Name + "…"}
	ctx, s, key, providers := m.ctx, m.deps.Store, m.key, m.providers

	return func() tea.Msg {
		msg := registrarsFoundMsg{zoneID: z.ID, keys: keys}

		for _, c := range candidates {
			err := askRegistrar(ctx, s, key, providers, c.account, c.zone)

			switch {
			case err == nil:
				msg.found = append(msg.found, c.zone)
			case errors.Is(err, errNoRegistrar), errors.Is(err, dnsprovider.ErrNotFound):
			default:
				msg.failed = append(msg.failed, "could not ask "+c.account.Label()+": "+err.Error())
			}
		}

		return msg
	}
}

// errNoRegistrar is an Account whose Provider is no Registrar Adapter.
var errNoRegistrar = errors.New("no Registrar Adapter")

// askRegistrar asks an Account whether it registers the domain of its
// Zone z and can publish its DS: errNoRegistrar when its Provider cannot,
// dnsprovider.ErrNotFound when it does not register it.
func askRegistrar(ctx context.Context, s *store.Store, key *vault.Key, providers *provider.Opener, a dns.Account, z dns.Zone) error {
	ctx, cancel := context.WithTimeout(ctx, registrarTimeout)
	defer cancel()

	p, _, err := accountProvider(ctx, s, key, providers, a)
	if err != nil {
		return err
	}

	r, ok := p.(dnsprovider.DSRegistrar)
	if !ok {
		return errNoRegistrar
	}

	_, err = r.DelegationSigners(ctx, provider.ModuleZone(z))

	return err
}

// dsPublishUpdate takes a result of publishing the DS: the Registrations
// found, or the write.
func (m *Model) dsPublishUpdate(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case registrarsFoundMsg:
		m.registrarsFound(msg)
	case dsPublishedMsg:
		return m.dsPublished(msg)
	}

	return nil
}

// registrarsFound takes the Registrations found for a Zone whose DS is
// to be published, unless the user moved on meanwhile, or it is no longer
// to be published, such as once the parent serves it: it asks to confirm
// publishing at the one found, lets the user pick one when several were,
// or shows the keys to copy when none was.
func (m *Model) registrarsFound(msg registrarsFoundMsg) {
	if m.open == nil || m.open.ID != msg.zoneID || m.dialogOpen() || m.dnssecStepOf(*m.open) != stepPublish {
		return
	}

	z := *m.open

	if len(msg.found) == 0 {
		m.notice = notice{text: "No Account in dnscli publishes the DS of " + z.Name + ": copy it into the registrar's panel."}
		if len(msg.failed) > 0 {
			m.notice = notice{text: "No Account in dnscli publishes the DS of " + z.Name + " (" + strings.Join(msg.failed, "; ") + ").", err: true}
		}

		m.openSigningKeys()

		return
	}

	m.notice = notice{}
	d := &dsPublish{zone: z, keys: msg.keys, registrar: msg.found[0]}
	m.dsPublish = d

	if len(msg.found) == 1 {
		return
	}

	// Several Accounts register it: the user picks one, each time.
	labels := make([]string, len(msg.found))
	for i, c := range msg.found {
		a, _ := m.account(c.AccountID)

		labels[i] = a.Label()
		if m.refreshOf(c.ID).notServed {
			labels[i] += " (" + registrationOnly + ")"
		}
	}

	d.picker = &picker{
		title:   "Publish the DS of " + z.Name + " at",
		options: labels,
		pick:    func(i int) { d.registrar = msg.found[i] },
	}
}

// dsPublishKey handles a key while publishing the DS is asked about:
// Enter publishes it, Esc cancels.
func (m *Model) dsPublishKey(msg tea.KeyMsg) tea.Cmd {
	d := m.dsPublish
	a := m.keys.action(msg.String())

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if d.busy {
		return nil
	}

	if d.picker != nil {
		if d.picker.key(a) {
			if a == ActionClose {
				m.dsPublish = nil
			}

			d.picker = nil
		}

		return nil
	}

	switch a {
	case ActionClose:
		m.dsPublish = nil
	case ActionSelect:
		return m.sendDS(d)
	}

	return nil
}

// sendDS publishes the keys of d at its Registration, replacing the DS
// the registry held.
func (m *Model) sendDS(d *dsPublish) tea.Cmd {
	a, ok := m.account(d.registrar.AccountID)
	if !ok {
		m.dsPublish = nil

		return nil
	}

	d.busy, d.err = true, ""
	ctx, s, key, providers := m.ctx, m.deps.Store, m.key, m.providers
	z, keys := d.registrar, provider.ModuleSigningKeys(d.keys)
	zoneID := d.zone.ID

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, registrarTimeout)
		defer cancel()

		p, _, err := accountProvider(ctx, s, key, providers, a)
		if err != nil {
			return dsPublishedMsg{zoneID: zoneID, err: err}
		}

		r, ok := p.(dnsprovider.DSRegistrar)
		if !ok {
			return dsPublishedMsg{zoneID: zoneID, err: errNoRegistrar}
		}

		return dsPublishedMsg{zoneID: zoneID, err: r.SetDelegationSigners(ctx, provider.ModuleZone(z), keys)}
	}
}

// dsPublished takes the result of publishing a Zone's DS: once published,
// the parent zone is checked for it again, as DNSSEC is On only once it
// serves it.
func (m *Model) dsPublished(msg dsPublishedMsg) tea.Cmd {
	d := m.dsPublish
	if d == nil || d.zone.ID != msg.zoneID {
		return nil
	}

	a, _ := m.account(d.registrar.AccountID)

	if msg.err != nil {
		d.busy = false
		d.err = publishError(d.zone.Name, a, msg.err)

		return nil
	}

	m.dsPublish = nil

	if m.dsSent == nil {
		m.dsSent = map[int64]bool{}
	}

	m.dsSent[d.zone.ID] = true
	m.notice = notice{text: "DS of " + d.zone.Name + " published at " + a.Label() + ": DNSSEC is On once the parent zone serves it."}
	// The re-checks of the parent start again.
	m.dnssecWatch = nil

	return m.checkParentDS(d.zone)
}

// publishError says why publishing the DS of zone at a failed: a
// registry that refuses the keys is named as such, as no retry helps.
func publishError(zone string, a dns.Account, err error) string {
	switch {
	case errors.Is(err, dnsprovider.ErrUnsupported):
		return "The registry of " + zone + " refuses this key's algorithm or form, which is not a dnscli error: " + err.Error()
	case errors.Is(err, dnsprovider.ErrNotFound):
		return a.Label() + " does not register " + zone + ": " + err.Error()
	}

	return "Could not publish the DS at " + a.Label() + ": " + err.Error()
}

// dsPublishView asks to confirm publishing the DS, naming the Account and
// the keys.
func (m *Model) dsPublishView() string {
	d := m.dsPublish
	if d.picker != nil {
		return d.picker.view(m.height)
	}
	a, _ := m.account(d.registrar.AccountID)

	notes := wrapNote(lipgloss.NewStyle(), "Publish the DS of "+d.zone.Name+" at "+a.Label()+"?", noteWidth)
	notes = append(notes, wrapNote(hintStyle, "It changes the Delegation: the parent zone serves it once the registry publishes it.", noteWidth)...)
	notes = append(notes, "")

	for _, k := range d.keys {
		if len(k.DS) > 0 {
			notes = append(notes, helpKeyStyle.Render("DS")+" "+k.DS[0])
		}
	}

	if d.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, d.err, noteWidth)...)
	}

	k := m.keys.keysFor
	hint := k(ActionSelect) + " publish · " + k(ActionClose) + " cancel"

	if d.busy {
		hint = "publishing…"
	}

	notes = append(notes, "", hintStyle.Render(hint))

	return dialogView("Publish the DS", nil, 0, false, notes)
}

// publishText says z's DS is to be published, and how: at the registrar
// while z may be written, else by copying the keys; or that it was
// published this session, and is waited for at the parent zone.
func (m *Model) publishText(z dns.Zone) string {
	if m.dsSent[z.ID] {
		return "DNSSEC: DS published, waiting for the parent zone"
	}

	k := m.keys.keysFor
	if m.writeRefusal(z) != "" {
		return "DNSSEC: publish the DS (" + k(ActionShowKeys) + " shows it)"
	}

	return "DNSSEC: publish the DS (" + k(ActionDNSSECStep) + " publishes it, " + k(ActionShowKeys) + " shows it)"
}
