package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// dsWrite writes the open Zone's DS at its registrar: the Account, among
// those holding a Zone of the same name, whose Provider is a Registrar
// Adapter and registers the domain. It publishes the keys, replaces a DS
// that matches none of them, or removes the DS before the Zone is
// unsigned, as its step says. The user confirms the Account and the set
// sent first, as it is a Delegation write (ADR-0007).
type dsWrite struct {
	// zone is the Live Zone whose DS is written.
	zone dns.Zone
	// step is the DNSSEC step the write takes: stepPublish, stepMismatch
	// or stepRemoveDS.
	step dnssecStep
	// keys are the keys sent: the whole set the registry holds once
	// written, none to remove the DS.
	keys []dns.SigningKey
	// registration is the Registration the DS is written at, once
	// picked.
	registration dns.Zone
	// picker lists the Registrations found to pick from, when several
	// were; nil once one is picked.
	picker *picker
	// unasked names the Accounts that could not be asked whether they
	// register the domain, and why: one may be its Registration too, so
	// the user is told before confirming another.
	unasked []string
	busy    bool
	err     string
}

// dsWriteVerb is how a DS write of step is said: "publish", "replace"
// or "remove".
func dsWriteVerb(step dnssecStep) string {
	switch step {
	case stepMismatch:
		return "replace"
	case stepRemoveDS:
		return "remove"
	}

	return "publish"
}

// dsWriteDoes is a DS write's verb as an Account does it: "publishes",
// "replaces" or "removes".
func dsWriteDoes(step dnssecStep) string {
	if step == stepPublish {
		return "publishes"
	}

	return dsWriteVerb(step) + "s"
}

// dsWriteDone is the past of a DS write's verb: "published", "replaced"
// or "removed".
func dsWriteDone(step dnssecStep) string {
	if step == stepPublish {
		return "published"
	}

	return dsWriteVerb(step) + "d"
}

// registrationsFoundMsg reports which Zones of a Live Zone's name are held
// by an Account that registers the domain and can write its DS.
type registrationsFoundMsg struct {
	zoneID int64
	// step is the DNSSEC step the Registration was looked for.
	step  dnssecStep
	keys  []dns.SigningKey
	found []dns.Zone
	// failed names each Account that could not be asked, other than
	// one that does not register the domain, and why: "porkbun.home
	// (porkbun: invalid API key)".
	failed []string
}

// dsWrittenMsg reports the result of writing a Zone's DS.
type dsWrittenMsg struct {
	zoneID int64
	err    error
}

// registrationCandidates are the Zones of z's name that may be its
// Registration, in the order they are tried: z itself, as one company
// often both hosts and registers a domain; then the Zones known to be
// registration only; then any other.
func (m *Model) registrationCandidates(z dns.Zone) []dns.Zone {
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

// registrationTimeout bounds asking one Account whether it registers a
// domain, and writing its DS there.
const registrationTimeout = 30 * time.Second

// writeDS takes z's DNSSEC step that writes its DS at the registrar,
// step: it looks for the Registration of its name, to write the DS there
// once the user confirms. Without one, or while z may not be written, it
// says how to do it by hand: the keys to copy into the registrar's panel,
// or to remove the DS there.
func (m *Model) writeDS(z dns.Zone, step dnssecStep) tea.Cmd {
	var keys []dns.SigningKey
	if step != stepRemoveDS {
		keys = m.signingKeysOf(z)
	}

	// The write gate is the Live Zone's: the DS is written at its
	// Registration, but a registration-only Zone can never be Validated,
	// as its Provider serves no Records for it.
	if m.writeRefusal(z) != "" || (step != stepRemoveDS && len(keys) == 0) {
		m.writeDSByHand(z, step, notice{})

		return nil
	}

	type candidate struct {
		zone    dns.Zone
		account dns.Account
	}

	var candidates []candidate

	for _, c := range m.registrationCandidates(z) {
		if a, ok := m.account(c.AccountID); ok {
			candidates = append(candidates, candidate{zone: c, account: a})
		}
	}

	m.notice = notice{text: "Looking for the registrar of " + z.Name + "…"}
	ctx, s, key, providers := m.ctx, m.deps.Store, m.key, m.providers

	return func() tea.Msg {
		msg := registrationsFoundMsg{zoneID: z.ID, step: step, keys: keys}

		for _, c := range candidates {
			// The Account registers the domain when it reads its DS.
			err := withRegistrar(ctx, s, key, providers, c.account, func(ctx context.Context, r dnsprovider.DSRegistrar) error {
				_, err := r.DelegationSigners(ctx, provider.ModuleZone(c.zone))

				return err
			})

			switch {
			case err == nil:
				msg.found = append(msg.found, c.zone)
			case errors.Is(err, errNoRegistrar), errors.Is(err, dnsprovider.ErrNotFound):
			default:
				msg.failed = append(msg.failed, c.account.Label()+" ("+err.Error()+")")
			}
		}

		return msg
	}
}

// writeDSByHand says how to take z's step at the registrar's panel, with
// n in the status bar: the keys to copy, or, to remove the DS, that
// dnscli watches the parent zone for it to go.
func (m *Model) writeDSByHand(z dns.Zone, step dnssecStep, n notice) {
	if step != stepRemoveDS {
		m.notice = n
		m.openSigningKeys()

		return
	}

	if n.text == "" {
		n.text = capitalise(removeDSFirst(z.Name)) + ": dnscli watches the parent zone for it to go."
	}

	m.notice = n
}

// byHand says how to take a DS write's step in the registrar's panel
// instead.
func byHand(step dnssecStep) string {
	if step == stepRemoveDS {
		return "remove it in the registrar's panel; dnscli watches the parent zone for it to go."
	}

	return "copy it into the registrar's panel."
}

// errNoRegistrar is an Account whose Provider is no Registrar Adapter.
var errNoRegistrar = errors.New("no Registrar Adapter")

// withRegistrar opens an Account's Provider as a Registrar Adapter and
// runs use on it, within registrationTimeout: errNoRegistrar when its
// Provider is none.
func withRegistrar(ctx context.Context, s *store.Store, key *vault.Key, providers *provider.Opener, a dns.Account,
	use func(ctx context.Context, r dnsprovider.DSRegistrar) error,
) error {
	ctx, cancel := context.WithTimeout(ctx, registrationTimeout)
	defer cancel()

	p, _, err := accountProvider(ctx, s, key, providers, a)
	if err != nil {
		return err
	}

	r, ok := p.(dnsprovider.DSRegistrar)
	if !ok {
		return errNoRegistrar
	}

	return use(ctx, r)
}

// dsWriteUpdate takes a result of writing the DS: the Registrations
// found, or the write.
func (m *Model) dsWriteUpdate(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case registrationsFoundMsg:
		m.registrationsFound(msg)
	case dsWrittenMsg:
		return m.dsWritten(msg)
	}

	return nil
}

// registrationsFound takes the Registrations found for a Zone whose DS is
// to be written, unless the user moved on meanwhile, or the step changed,
// such as once the parent serves the DS published: it asks to confirm the
// write at the one found, lets the user pick one when several were, or
// says how to do it by hand when none was.
func (m *Model) registrationsFound(msg registrationsFoundMsg) {
	if m.open == nil || m.open.ID != msg.zoneID || m.dialogOpen() || m.dnssecStepOf(*m.open) != msg.step {
		return
	}

	z := *m.open
	verb := dsWriteVerb(msg.step)

	if len(msg.found) == 0 {
		none := "Account in dnscli " + dsWriteDoes(msg.step) + " the DS of " + z.Name + ": " + byHand(msg.step)

		n := notice{text: "No " + none}
		if len(msg.failed) > 0 {
			n = notice{text: "Could not ask " + strings.Join(msg.failed, ", ") + ", and no other " + none, err: true}
		}

		m.writeDSByHand(z, msg.step, n)

		return
	}

	m.notice = notice{}
	d := &dsWrite{zone: z, step: msg.step, keys: msg.keys, registration: msg.found[0], unasked: msg.failed}
	m.dsWrite = d

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
		title:   capitalise(verb) + " the DS of " + z.Name + " at",
		options: labels,
		pick:    func(i int) { d.registration = msg.found[i] },
	}
}

// dsWriteKey handles a key while writing the DS is asked about: Enter
// writes it, Esc cancels.
func (m *Model) dsWriteKey(msg tea.KeyMsg) tea.Cmd {
	d := m.dsWrite
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
				m.dsWrite = nil
			}

			d.picker = nil
		}

		return nil
	}

	switch a {
	case ActionClose:
		m.dsWrite = nil
	case ActionSelect:
		return m.sendDS(d)
	}

	return nil
}

// sendDS sends the keys of d to its Registration, replacing the DS the
// registry held: none removes it.
func (m *Model) sendDS(d *dsWrite) tea.Cmd {
	a, ok := m.account(d.registration.AccountID)
	if !ok {
		m.dsWrite = nil

		return nil
	}

	d.busy, d.err = true, ""
	ctx, s, key, providers := m.ctx, m.deps.Store, m.key, m.providers
	z, keys := d.registration, provider.ModuleSigningKeys(d.keys)
	zoneID := d.zone.ID

	return func() tea.Msg {
		err := withRegistrar(ctx, s, key, providers, a, func(ctx context.Context, r dnsprovider.DSRegistrar) error {
			return r.SetDelegationSigners(ctx, provider.ModuleZone(z), keys)
		})

		return dsWrittenMsg{zoneID: zoneID, err: err}
	}
}

// dsWritten takes the result of writing a Zone's DS: once written, the
// parent zone is checked for it again, as the next step comes only once
// it serves the new set.
func (m *Model) dsWritten(msg dsWrittenMsg) tea.Cmd {
	d := m.dsWrite
	if d == nil || d.zone.ID != msg.zoneID {
		return nil
	}

	a, _ := m.account(d.registration.AccountID)

	if msg.err != nil {
		d.busy = false
		d.err = dsWriteError(d.zone.Name, d.step, a, msg.err)

		return nil
	}

	m.dsWrite = nil

	if m.dsSent == nil {
		m.dsSent = map[int64]dnssecStep{}
	}

	m.dsSent[d.zone.ID] = d.step

	next := "DNSSEC is On once the parent zone serves it."
	if d.step == stepRemoveDS {
		next = "dnscli unsigns the Zone once the parent zone stops serving it and its TTL passes."
	}

	m.notice = notice{text: "DS of " + d.zone.Name + " " + dsWriteDone(d.step) + " at " + a.Label() + ": " + next}
	// The re-checks of the parent start again.
	m.dnssecWatch = nil

	return m.checkParentDS(d.zone)
}

// dsWriteError says why writing the DS of zone at a, for step, failed: a
// registry that refuses the keys is named as such, as no retry helps.
func dsWriteError(zone string, step dnssecStep, a dns.Account, err error) string {
	switch {
	case errors.Is(err, dnsprovider.ErrUnsupported):
		return "The registry of " + zone + " refuses this key's algorithm or form, which is not a dnscli error: " + err.Error()
	case errors.Is(err, dnsprovider.ErrNotFound):
		return a.Label() + " does not register " + zone + ": " + err.Error()
	}

	return "Could not " + dsWriteVerb(step) + " the DS at " + a.Label() + ": " + err.Error()
}

// dsWriteView asks to confirm writing the DS, naming the Account and the
// exact set sent, or, to remove it, the DS the parent zone serves.
func (m *Model) dsWriteView() string {
	d := m.dsWrite
	if d.picker != nil {
		return d.picker.view(m.height)
	}

	a, _ := m.account(d.registration.AccountID)
	verb := dsWriteVerb(d.step)
	title := capitalise(verb) + " the DS"

	why := "It changes the Delegation: the parent zone serves it once the registry publishes it."

	switch d.step {
	case stepMismatch:
		why = "It changes the Delegation: the DS the parent zone serves matches none of the keys, and these replace it."
	case stepRemoveDS:
		why = "It changes the Delegation: dnscli unsigns the Zone once the parent zone stops serving the DS and its TTL passes."
	}

	notes := wrapNote(lipgloss.NewStyle(), title+" of "+d.zone.Name+" at "+a.Label()+"?", noteWidth)
	notes = append(notes, wrapNote(hintStyle, why, noteWidth)...)
	notes = append(notes, "")

	// The values wrap only when the terminal cannot hold them.
	valueW := max(m.width-16, 20)

	if d.step == stepRemoveDS {
		// Every DS record the parent zone serves goes.
		for _, ds := range m.parentDS[nameKey(d.zone.Name)].ds {
			notes = append(notes, helpKeyStyle.Render("DS"))
			notes = append(notes, strings.Split(ansi.Hardwrap(ds, valueW, false), "\n")...)
		}
	}

	// Every key shows as it is sent, its DNSKEY and each of its DS
	// records, whichever form the registrar takes (ADR-0007).
	for i, k := range d.keys {
		if i > 0 {
			notes = append(notes, "")
		}

		notes = append(notes, helpKeyStyle.Render("DNSKEY"))
		notes = append(notes, strings.Split(ansi.Hardwrap(k.DNSKEY, valueW, false), "\n")...)

		for _, ds := range k.DS {
			notes = append(notes, helpKeyStyle.Render("DS"))
			notes = append(notes, strings.Split(ansi.Hardwrap(ds, valueW, false), "\n")...)
		}
	}

	for _, u := range d.unasked {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, "Could not ask "+u+" whether it registers "+d.zone.Name+" too.", noteWidth)...)
	}

	if d.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, d.err, noteWidth)...)
	}

	k := m.keys.keysFor
	hint := k(ActionSelect) + " " + verb + " · " + k(ActionClose) + " cancel"

	if d.busy {
		hint = strings.TrimSuffix(verb, "e") + "ing…"
	}

	notes = append(notes, "", hintStyle.Render(hint))

	return dialogView(title, nil, 0, false, notes)
}

// dsSentAt reports whether z's DS was written at its registrar this
// session for step, and is waited for at the parent zone.
func (m *Model) dsSentAt(z dns.Zone, step dnssecStep) bool {
	s, ok := m.dsSent[z.ID]

	return ok && s == step
}

// publishText says z's DS is to be published, and how: at the registrar
// while z may be written, else by copying the keys; or that it was
// published this session, and is waited for at the parent zone.
func (m *Model) publishText(z dns.Zone) string {
	if m.dsSentAt(z, stepPublish) {
		return "DNSSEC: DS published, waiting for the parent zone"
	}

	k := m.keys.keysFor
	if m.writeRefusal(z) != "" {
		return "DNSSEC: publish the DS (" + k(ActionShowKeys) + " shows it)"
	}

	return "DNSSEC: publish the DS (" + k(ActionDNSSECStep) + " publishes it, " + k(ActionShowKeys) + " shows it)"
}

// removeDSText says the DS of z, being turned off, is to be removed
// first, and how while z may be written; or that it was removed this
// session, and its going is waited for at the parent zone.
func (m *Model) removeDSText(z dns.Zone) string {
	if m.dsSentAt(z, stepRemoveDS) {
		return "DNSSEC: DS removed, waiting for the parent zone"
	}

	text := "DNSSEC: remove the DS at the registrar to turn it off"
	if m.writeRefusal(z) != "" {
		return text
	}

	return text + " (" + m.keys.keysFor(ActionDNSSECStep) + " removes it)"
}

// mismatchText says, in its style, that the DS z's parent serves matches
// none of its keys, and how to replace it while z may be written and its
// keys are read; or that it was replaced this session, and the new one is
// waited for at the parent zone.
func (m *Model) mismatchText(z dns.Zone) (string, lipgloss.Style) {
	if m.dsSentAt(z, stepMismatch) {
		return "DNSSEC: DS replaced, waiting for the parent zone", badgePendingStyle
	}

	text := "DNSSEC: DS does not match"
	if m.writeRefusal(z) != "" || len(m.signingKeysOf(z)) == 0 {
		return text, errorStyle
	}

	k := m.keys.keysFor

	return text + " (" + k(ActionDNSSECStep) + " replaces it, " + k(ActionShowKeys) + " shows the keys)", errorStyle
}
