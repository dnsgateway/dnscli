package tui

import (
	"context"
	"errors"
	"slices"
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

// dsWriteKind is what a DS write does at the registrar.
type dsWriteKind int

const (
	// dsPublish publishes the keys of a signed Zone.
	dsPublish dsWriteKind = iota
	// dsReplace replaces a DS that matches none of the Zone's keys with
	// them.
	dsReplace
	// dsRemove removes the DS of a Zone being turned off, before it is
	// unsigned.
	dsRemove
)

// dsWriteWords is how a kind of DS write is said, and the DNSSEC step
// that takes it.
type dsWriteWords struct {
	// step is the DNSSEC step that takes it.
	step dnssecStep
	// sendsKeys is set when it sends the Zone's keys; else it sends none,
	// removing the DS.
	sendsKeys bool
	// verb, does, done and doing are its verb: "publish", "publishes",
	// "published" and "publishing…".
	verb, does, done, doing string
	// todo says, in the Zone Status bar, that it is to be taken.
	todo string
	// shows is what ActionShowKeys shows meanwhile, when the Zone's keys
	// are read; empty when it shows nothing of use.
	shows string
	// why says, in its confirmation, how it changes the Delegation.
	why string
	// next says what follows once it is written.
	next string
	// byHand says how to do it in the registrar's panel instead.
	byHand string
}

// watchesDSGo says that dnscli sees a DS removed by hand.
const watchesDSGo = "dnscli watches the parent zone for it to go."

const (
	// onOnceServed is what follows writing a Zone's keys at its registrar.
	onOnceServed = "DNSSEC is On once the parent zone serves it."
	// unsignOnceGone is what follows removing the DS of a Zone being
	// turned off.
	unsignOnceGone = "dnscli unsigns the Zone once the parent zone stops serving the DS and its TTL passes."
	// copyKeysByHand is how to write a Zone's keys at its registrar by hand.
	copyKeysByHand = "copy it into the registrar's panel."
)

// dsWrites says each kind of DS write.
var dsWrites = [...]dsWriteWords{
	dsPublish: {
		step: stepPublish, sendsKeys: true,
		verb: "publish", does: "publishes", done: "published", doing: "publishing…",
		todo: "DNSSEC: publish the DS", shows: "it",
		why:  "the parent zone serves it once the registry publishes it.",
		next: onOnceServed, byHand: copyKeysByHand,
	},
	dsReplace: {
		step: stepMismatch, sendsKeys: true,
		verb: "replace", does: "replaces", done: "replaced", doing: "replacing…",
		todo: "DNSSEC: DS does not match", shows: "the keys",
		why:  "the DS the parent zone serves matches none of the keys, and these replace it.",
		next: onOnceServed, byHand: copyKeysByHand,
	},
	dsRemove: {
		step: stepRemoveDS,
		verb: "remove", does: "removes", done: "removed", doing: "removing…",
		todo: "DNSSEC: remove the DS at the registrar to turn it off",
		why:  unsignOnceGone,
		next: unsignOnceGone, byHand: "remove it in the registrar's panel; " + watchesDSGo,
	},
}

// dsWriteKindOf is the kind of DS write step takes.
func dsWriteKindOf(step dnssecStep) dsWriteKind {
	switch step {
	case stepMismatch:
		return dsReplace
	case stepRemoveDS:
		return dsRemove
	}

	return dsPublish
}

// dsWrite writes the open Zone's DS at its registrar: the Account, among
// those holding a Zone of the same name, whose Provider is a Registrar
// Adapter and registers the domain. It publishes the keys, replaces a DS
// that matches none of them, or removes the DS before the Zone is
// unsigned, as its kind says. The user confirms the Account and the set
// sent first, as it is a Delegation write (ADR-0007).
type dsWrite struct {
	// zone is the Live Zone whose DS is written.
	zone dns.Zone
	// step is the DNSSEC step the write takes.
	step dnssecStep
	kind dsWriteKind
	// keys are the keys sent, as shown: the whole set the registry holds
	// once written, none to remove the DS.
	keys []dns.SigningKey
	// removed is the DS the parent zone served when the write was asked,
	// shown as what a removal ends.
	removed []string
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

// registrationsFoundMsg reports which Zones of a Live Zone's name are held
// by an Account that registers the domain and can write its DS.
type registrationsFoundMsg struct {
	zoneID int64
	// step is the DNSSEC step the Registration was looked for.
	step  dnssecStep
	kind  dsWriteKind
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

// dsWritable reports whether z's DS may be written at its registrar for
// kind, given its keys: while z may be written, and, for a write that
// sends them, once they are read.
//
// The write gate is the Live Zone's: the DS is written at its
// Registration, but a registration-only Zone can never be Validated, as
// its Provider serves no Records for it.
func (m *Model) dsWritable(z dns.Zone, kind dsWriteKind, keys []dns.SigningKey) bool {
	return m.writeRefusal(z) == "" && (!dsWrites[kind].sendsKeys || len(keys) > 0)
}

// writeDS takes z's DNSSEC step that writes its DS at the registrar,
// step: it looks for the Registration of its name, to write the DS there
// once the user confirms. Without one, or while z may not be written, it
// says how to do it by hand: the keys to copy into the registrar's panel,
// or to remove the DS there.
func (m *Model) writeDS(z dns.Zone, step dnssecStep) tea.Cmd {
	kind := dsWriteKindOf(step)

	var keys []dns.SigningKey
	if dsWrites[kind].sendsKeys {
		keys = m.signingKeysOf(z)
	}

	if !m.dsWritable(z, kind, keys) {
		m.writeDSByHand(z, kind, notice{})

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
		msg := registrationsFoundMsg{zoneID: z.ID, step: step, kind: kind, keys: keys}

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

// writeDSByHand says how to write z's DS of kind in the registrar's panel,
// with n in the status bar: the keys to copy, or, to remove the DS, that
// dnscli watches the parent zone for it to go.
func (m *Model) writeDSByHand(z dns.Zone, kind dsWriteKind, n notice) {
	if dsWrites[kind].sendsKeys {
		m.notice = n
		m.openSigningKeys()

		return
	}

	if n.text == "" {
		n.text = capitalise(removeDSFirst(z.Name)) + ": " + watchesDSGo
	}

	m.notice = n
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
	w := dsWrites[msg.kind]

	if len(msg.found) == 0 {
		none := "Account in dnscli " + w.does + " the DS of " + z.Name + ": " + w.byHand

		n := notice{text: "No " + none}
		if len(msg.failed) > 0 {
			n = notice{text: "Could not ask " + strings.Join(msg.failed, ", ") + ", and no other " + none, err: true}
		}

		m.writeDSByHand(z, msg.kind, n)

		return
	}

	m.notice = notice{}
	d := &dsWrite{
		zone: z, step: msg.step, kind: msg.kind, keys: msg.keys,
		registration: msg.found[0], unasked: msg.failed,
	}

	if !w.sendsKeys {
		d.removed = slices.Clone(m.parentDS[nameKey(z.Name)].ds)
	}

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
		title:   capitalise(w.verb) + " the DS of " + z.Name + " at",
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
	w := dsWrites[d.kind]

	if msg.err != nil {
		d.busy = false
		d.err = dsWriteError(d.zone.Name, w, a, msg.err)

		return nil
	}

	m.dsWrite = nil

	if m.dsSent == nil {
		m.dsSent = map[int64]dsWriteKind{}
	}

	m.dsSent[d.zone.ID] = d.kind

	m.notice = notice{text: "DS of " + d.zone.Name + " " + w.done + " at " + a.Label() + ": " + w.next}
	// The re-checks of the parent start again.
	m.dnssecWatch = nil

	return m.checkParentDS(d.zone)
}

// dsWriteError says why writing the DS of zone at a, as w, failed: a
// registry that refuses the keys is named as such, as no retry helps.
func dsWriteError(zone string, w dsWriteWords, a dns.Account, err error) string {
	switch {
	case errors.Is(err, dnsprovider.ErrUnsupported):
		return "The registry of " + zone + " refuses this key's algorithm or form, which is not a dnscli error: " + err.Error()
	case errors.Is(err, dnsprovider.ErrNotFound):
		return a.Label() + " does not register " + zone + ": " + err.Error()
	}

	return "Could not " + w.verb + " the DS at " + a.Label() + ": " + err.Error()
}

// dsWriteView asks to confirm writing the DS, naming the Account and
// exactly what was shown when it was asked: the set sent, or, to remove
// it, the DS the parent zone served.
func (m *Model) dsWriteView() string {
	d := m.dsWrite
	if d.picker != nil {
		return d.picker.view(m.height)
	}

	a, _ := m.account(d.registration.AccountID)
	w := dsWrites[d.kind]
	title := capitalise(w.verb) + " the DS"

	notes := wrapNote(lipgloss.NewStyle(), title+" of "+d.zone.Name+" at "+a.Label()+"?", noteWidth)
	notes = append(notes, wrapNote(hintStyle, "It changes the Delegation: "+w.why, noteWidth)...)
	notes = append(notes, "")

	// The values wrap only when the terminal cannot hold them.
	valueW := max(m.width-16, 20)
	value := func(name, v string) {
		notes = append(notes, helpKeyStyle.Render(name))
		notes = append(notes, strings.Split(ansi.Hardwrap(v, valueW, false), "\n")...)
	}

	// Every DS record the parent zone served goes.
	for _, ds := range d.removed {
		value("DS", ds)
	}

	// Every key shows as it is sent, its DNSKEY and each of its DS
	// records, whichever form the registrar takes (ADR-0007).
	for i, k := range d.keys {
		if i > 0 {
			notes = append(notes, "")
		}

		value("DNSKEY", k.DNSKEY)

		for _, ds := range k.DS {
			value("DS", ds)
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
	hint := k(ActionSelect) + " " + w.verb + " · " + k(ActionClose) + " cancel"

	if d.busy {
		hint = w.doing
	}

	notes = append(notes, "", hintStyle.Render(hint))

	return dialogView(title, nil, 0, false, notes)
}

// dsSentFor reports the kind of DS write sent this session to z's
// registrar, when it was for step: it is waited for at the parent zone.
func (m *Model) dsSentFor(z dns.Zone, step dnssecStep) (dsWriteKind, bool) {
	k, ok := m.dsSent[z.ID]

	return k, ok && dsWrites[k].step == step
}

// dsStepText says, for the Zone Status bar, that z's step writing its DS
// at the registrar is to be taken, and how: by ActionDNSSECStep while it
// may be written, else by hand, with the keys ActionShowKeys shows; or that
// it was written this session, and is waited for at the parent zone.
func (m *Model) dsStepText(z dns.Zone, step dnssecStep) string {
	if sent, ok := m.dsSentFor(z, step); ok {
		return "DNSSEC: DS " + dsWrites[sent].done + ", waiting for the parent zone"
	}

	kind := dsWriteKindOf(step)
	w, keys := dsWrites[kind], m.signingKeysOf(z)
	k := m.keys.keysFor

	var how []string
	if m.dsWritable(z, kind, keys) {
		how = append(how, k(ActionDNSSECStep)+" "+w.does+" it")
	}

	if w.shows != "" && len(keys) > 0 {
		how = append(how, k(ActionShowKeys)+" shows "+w.shows)
	}

	if len(how) == 0 {
		return w.todo
	}

	return w.todo + " (" + strings.Join(how, ", ") + ")"
}
