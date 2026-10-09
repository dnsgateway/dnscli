package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/store"
)

// offboardDialog is the dialog that Offboards the Zone highlighted in the
// Domains pane, a Zone write that cannot be undone: the user confirms
// that it stops DNS hosting of the Zone at its Account and drops its
// Records there, leaving the domain's registration alone. The Live Zone
// is never Offboarded: it is refused when the dialog would open, and
// again, from a Delegation resolved anew and the Zone read anew from its
// Provider, just before the delete is sent.
type offboardDialog struct {
	zone    dns.Zone
	account dns.Account
	// p is the Account's Provider, a dnsprovider.ZoneDeleter.
	p    dnsprovider.Provider
	busy bool // the Zone is being Offboarded
	// cancel stops the Offboarding in flight; nil when none is.
	cancel context.CancelFunc
	err    string
}

// offboardOpenedMsg reports the Provider opened for the Offboard dialog;
// p is nil when it failed.
type offboardOpenedMsg struct {
	zone    dns.Zone
	account dns.Account
	p       dnsprovider.Provider
	err     error
}

// zoneOffboardedMsg reports an Offboarding.
type zoneOffboardedMsg struct {
	zone    dns.Zone
	account dns.Account
	// refused is why the Zone was not Offboarded after all: it is, or may
	// be, the Live Zone. Nothing was sent.
	refused error
	// resolved is set when the Delegation was resolved anew, to delegated
	// or, failing, resolveErr, which the session then records as its own.
	resolved   bool
	delegated  []string
	resolveErr error
	// gone is set when the Provider no longer had the Zone: it was
	// Offboarded, but not, as far as dnscli can tell, by this delete.
	gone bool
	// err is why the Offboarding failed.
	err error
	// sent is set when the delete was sent, so it may have been taken.
	sent bool
	// cancelled is set when the user cancelled it.
	cancelled bool
	// held is set when reading back a failed delete found the Zone still
	// at the Provider.
	held bool
	// readBack is why reading back a failed delete failed.
	readBack error
	// saveErr is why the Zone could not be removed from the cache, after
	// its Provider Offboarded it.
	saveErr error
}

// openOffboard opens the Offboard dialog for the Zone highlighted in the
// Domains pane. Offboarding is a Zone write: a sealed session Unseals
// first, the Account's Provider must be able to Offboard, and the Zone
// must be known not to be the Live Zone.
func (m *Model) openOffboard() tea.Cmd {
	if len(m.visible) == 0 {
		m.notice = notice{text: "Highlight a Zone in the Domains pane first.", err: true}

		return nil
	}

	z := m.visible[m.cursor[paneDomains]]

	a, ok := m.account(z.AccountID)
	if !ok {
		return nil
	}

	// Whether the Provider can is known only once it is opened, which
	// needs secrets; a Provider type that cannot is refused before asking
	// for them.
	if !m.accountCan(a, dnsprovider.CapZoneDeleter) {
		m.notice = notice{text: cannotOffboard(a), err: true}

		return nil
	}

	if err := m.offboardRefusal(z, a); err != nil {
		m.notice = notice{text: err.Error(), err: true}

		return nil
	}

	return m.withSecrets(func() tea.Cmd { return m.openOffboardOf(z.ID) })
}

// cannotOffboard is the notice for an Account whose Provider cannot
// Offboard a Zone.
func cannotOffboard(a dns.Account) string {
	return a.Label() + " cannot Offboard a Zone: its Provider does not stop hosting a Zone apart from its domain's registration."
}

// offboardRefusal says why the session may not Offboard z: work on z is
// in flight or pending, or, from what the session knows of z's
// Delegation and Hosting State, z is the Live Zone, may be, or whether it
// is is unknown. nil when z is known not to be the Live Zone.
func (m *Model) offboardRefusal(z dns.Zone, a dns.Account) error {
	if err := m.offboardBusy(z); err != nil {
		return err
	}

	if m.deps.Resolver == nil {
		return liveUnknownError(z.Name, "dnscli has no resolver to read its Delegation with")
	}

	d, ok := m.delegations[nameKey(z.Name)]

	switch {
	case !ok || d.state == lookupQueued || d.state == lookupResolving:
		return liveUnknownError(z.Name, "its Delegation is not resolved yet")
	case d.state == lookupFailed:
		return liveUnknownError(z.Name, "its Delegation could not be resolved: "+d.err.Error())
	}

	if err := liveRefusal(z.Name, a, z.Nameservers, d.ns); err != nil {
		return err
	}

	return hostingRefusal(z.Name, a, z.State)
}

// offboardingIn reports whether a Zone of the Account of that ID is being
// Offboarded.
func (m *Model) offboardingIn(accountID int64) bool {
	for _, id := range m.offboarding {
		if id == accountID {
			return true
		}
	}

	return false
}

// isOffboarding reports whether the Zone of that ID is being Offboarded.
func (m *Model) isOffboarding(zoneID int64) bool {
	_, ok := m.offboarding[zoneID]

	return ok
}

// offboardBusy says why z may not be Offboarded while the session works
// on it: an Apply or a Refresh of z in flight would write to the cache a
// Zone the Offboarding removes from it. A pending Changeset refuses too,
// rather than being discarded: it is work the user has not applied, and
// discarding it takes one key in the preview, whereas Offboarding cannot
// be undone. nil when none is.
// A re-listing of z's Account in flight refuses too: its listing, taken
// before the delete and cached after it, would bring z back. The other
// way round, no re-listing of an Account starts while one of its Zones
// is Offboarded (see relistAccount).
func (m *Model) offboardBusy(z dns.Zone) error {
	if m.relisting[z.AccountID] {
		label := "its Account"
		if a, ok := m.account(z.AccountID); ok {
			label = a.Label()
		}

		return fmt.Errorf("Offboarding is refused while %s is being re-listed: try again once it is.", label)
	}

	switch {
	case m.applying[z.ID]:
		return fmt.Errorf("Offboarding is refused while changes to %s are being applied.", z.Name)
	case m.refreshes[z.ID] != nil && m.refreshes[z.ID].pending:
		return fmt.Errorf("Offboarding is refused while %s is being Refreshed: try again once it is.", z.Name)
	case m.pendingOf(z.ID) != nil:
		return fmt.Errorf("Offboarding is refused: %s has a pending Changeset. Apply or discard it first (%s preview).",
			z.Name, m.keys.keysFor(ActionPreviewChanges))
	}

	return nil
}

// hostingRefusal says why a Zone of name at Account a, in Hosting State
// state, may not be Offboarded, whatever its nameservers: a Zone its
// Provider serves is reached by the Delegation, through custom
// nameservers, a partial setup or resolvers still caching an older
// Delegation, and of one whose state is unknown that is not known. Only a
// pending or moved Zone may be. nil when it may.
func hostingRefusal(name string, a dns.Account, state dnsprovider.HostingState) error {
	switch state {
	case dnsprovider.HostingPending, dnsprovider.HostingMoved:
		return nil
	case dnsprovider.HostingActive:
		return fmt.Errorf("Offboarding is refused: %s serves %s (active), so the Delegation still reaches it, "+
			"through custom nameservers, a partial setup or resolvers caching an older Delegation. "+
			"Only a pending or moved Zone is Offboarded.", a.Label(), name)
	}

	return fmt.Errorf("Offboarding is refused while the Hosting State of %s at %s is unknown: re-list the Account. "+
		"Only a pending or moved Zone is Offboarded.", name, a.Label())
}

// liveRefusal says why a Zone of name at Account a, with nameservers ns,
// may not be Offboarded while the Delegation names delegated, a nameSet:
// its nameservers are the delegated ones, so it is the Live Zone; it
// shares one with them, so its Provider may still answer for the name;
// or they are not known, so whether it is the Live Zone is not. nil when
// it may.
func liveRefusal(name string, a dns.Account, ns, delegated []string) error {
	switch livenessOf(ns, delegated) {
	case liveUnknown:
		return liveUnknownError(name, "the nameservers "+a.Label()+" assigned it are not known: re-list the Account")
	case liveYes:
		return fmt.Errorf("%s is the Live Zone of %s: its Delegation names the nameservers %s assigned it, "+
			"so Offboarding it would stop the domain resolving. Move the Delegation first.", name, name, a.Label())
	}

	// Not the Live Zone by the rule, but its Provider may still answer
	// for the name through a nameserver they share.
	for _, n := range nameSet(ns) {
		if slices.Contains(delegated, n) {
			return fmt.Errorf("Offboarding is refused: the Delegation of %s names %s, which %s assigned it, "+
				"so %s may still answer for %s.", name, n, a.Label(), a.Label(), name)
		}
	}

	return nil
}

// liveUnknownError refuses Offboarding a Zone whose name's Live Zone is
// not known, saying why.
func liveUnknownError(name, why string) error {
	return fmt.Errorf("Offboarding is refused while the Live Zone of %s is unknown: %s. "+
		"The Live Zone is never Offboarded.", name, why)
}

// openOffboardOf opens the Provider of a Zone's Account, as it is now,
// for the Offboard dialog.
func (m *Model) openOffboardOf(zoneID int64) tea.Cmd {
	z, ok := m.zoneByID[zoneID]
	if !ok {
		return nil
	}

	a, ok := m.account(z.AccountID)
	if !ok {
		return nil
	}

	ctx, s, key, providers := m.ctx, m.deps.Store, m.key, m.providers

	return func() tea.Msg {
		p, _, err := accountProvider(ctx, s, key, providers, a)

		return offboardOpenedMsg{zone: z, account: a, p: p, err: err}
	}
}

// tenantUse opens the Offboard dialog again, for the Zone as it is by
// then.
func (msg offboardOpenedMsg) tenantUse(m *Model) (int64, func() tea.Cmd, error) {
	return msg.account.ID, func() tea.Cmd { return m.openOffboardOf(msg.zone.ID) }, msg.err
}

// offboardOpened opens the dialog, unless the Provider could not be
// opened or cannot Offboard, the Zone left the cache or may be the Live
// Zone by now, or another dialog opened meanwhile.
func (m *Model) offboardOpened(msg offboardOpenedMsg) {
	var choice *provider.TenantChoiceError

	switch {
	case errors.As(msg.err, &choice):
		return // the Tenant picker asks, and opens the dialog again
	case msg.err != nil:
		m.notice = notice{text: "Could not open " + msg.account.Label() + ": " + msg.err.Error(), err: true}

		return
	case !dnsprovider.CanDeleteZone(msg.p):
		m.notice = notice{text: cannotOffboard(msg.account), err: true}

		return
	case m.dialogOpen() || m.tenantPick != nil:
		return
	}

	z, ok := m.zoneByID[msg.zone.ID]
	if !ok {
		return
	}

	if err := m.offboardRefusal(z, msg.account); err != nil {
		m.notice = notice{text: err.Error(), err: true}

		return
	}

	m.offboard = &offboardDialog{zone: z, account: msg.account, p: msg.p}
}

// offboardKey handles a key while the Offboard dialog is open: Enter
// Offboards the Zone, Esc closes the dialog, or cancels an Offboarding in
// flight, which is then read back as one whose answer was lost.
func (m *Model) offboardKey(msg tea.KeyMsg) tea.Cmd {
	d := m.offboard
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

	switch a {
	case ActionClose:
		m.offboard = nil
	case ActionSelect:
		// The session's view of the Delegation may have changed since
		// the dialog opened.
		if err := m.offboardRefusal(d.zone, d.account); err != nil {
			m.offboard = nil
			m.notice = notice{text: err.Error(), err: true}

			return nil
		}

		d.busy, d.err = true, ""

		if m.offboarding == nil {
			m.offboarding = map[int64]int64{}
		}

		m.offboarding[d.zone.ID] = d.account.ID

		return m.deleteZone(d)
	}

	return nil
}

// offboardTimeout bounds an Offboarding, its checks and the delete
// together, and, apart, the read-back of a delete whose answer was lost.
const offboardTimeout = time.Minute

// deleteZone Offboards the Zone confirmed, and removes it from the cache
// with its Records. Just before the delete it reads the Zone from its
// Provider and resolves its Delegation anew, and refuses, sending
// nothing, a Zone that is, or may be, the Live Zone by then: the session's
// view of either may be minutes old. A Zone its Provider no longer has
// is removed from the cache without a delete. A delete that failed is
// read back, whatever the failure: a Zone gone is Offboarded.
func (m *Model) deleteZone(d *offboardDialog) tea.Cmd {
	parent, s, r, p, a, z := m.ctx, m.deps.Store, m.deps.Resolver, d.p, d.account, d.zone

	ctx, cancel := context.WithTimeout(parent, offboardTimeout)
	d.cancel = cancel

	return func() tea.Msg {
		defer cancel()

		msg := zoneOffboardedMsg{zone: z, account: a}

		held, ok, err := heldZone(ctx, p, z)

		switch {
		case err != nil:
			msg.err, msg.cancelled = err, errors.Is(err, context.Canceled)

			return msg
		case !ok:
			msg.gone = true
			msg.saveErr = uncache(parent, s, z)

			return msg
		}

		if r == nil {
			msg.refused = liveUnknownError(z.Name, "dnscli has no resolver to read its Delegation with")

			return msg
		}

		ns, rerr := r.NS(ctx, nameKey(z.Name))
		if ctx.Err() == nil {
			msg.resolved, msg.delegated, msg.resolveErr = true, ns, rerr
		}

		if msg.refused = checkNotLive(a, z, held, ns, rerr); msg.refused != nil {
			return msg
		}

		msg.sent = true

		err = p.(dnsprovider.ZoneDeleter).DeleteZone(ctx, held)
		if err != nil {
			// Whatever the failure, the delete was sent, and the
			// module retries it: an earlier try may have taken it.
			rctx, rcancel := context.WithTimeout(parent, offboardTimeout)
			_, still, rerr := heldZone(rctx, p, z)

			rcancel()

			if rerr != nil || still {
				msg.err, msg.cancelled, msg.readBack, msg.held = err, errors.Is(err, context.Canceled), rerr, still

				return msg
			}

			// Gone: this delete, whose answer was lost, Offboarded it,
			// unless the Provider answered that it did not, as when it
			// had no such Zone to begin with.
			msg.gone = answered(err)
		}

		msg.saveErr = uncache(parent, s, z)

		return msg
	}
}

// heldZone reads z from its Account's Provider, by its Provider zone ID
// among the Zones of its name: ok is false when the Provider no longer
// has it.
func heldZone(ctx context.Context, p dnsprovider.Provider, z dns.Zone) (dnsprovider.Zone, bool, error) {
	zones, err := p.Zones(ctx, dnsprovider.ZoneFilter{Contains: z.Name})
	if err != nil {
		return dnsprovider.Zone{}, false, err
	}

	for _, mz := range zones {
		if mz.ID == z.ProviderID {
			return mz, true, nil
		}
	}

	return dnsprovider.Zone{}, false, nil
}

// checkNotLive refuses z, as its Provider has it now (held) and as
// cached, when either is, or may be, the Live Zone of the Delegation the
// resolver gave just now (ns, or the error resolving it), or when its
// Provider serves it now, or does not say whether it does.
func checkNotLive(a dns.Account, z dns.Zone, held dnsprovider.Zone, ns []string, err error) error {
	if err == nil && len(ns) == 0 {
		err = errNoNameservers
	}

	if err != nil {
		return liveUnknownError(z.Name, "its Delegation could not be resolved again: "+err.Error())
	}

	delegated := nameSet(ns)

	if err := liveRefusal(z.Name, a, held.Nameservers, delegated); err != nil {
		return err
	}

	if len(z.Nameservers) > 0 {
		if err := liveRefusal(z.Name, a, z.Nameservers, delegated); err != nil {
			return err
		}
	}

	return hostingRefusal(z.Name, a, held.State)
}

// answered reports whether a failed delete was the Provider's answer
// that it did not act, such as a not-found or a refusal, rather than a
// failure without one, such as a lost connection, a timeout, a cancel or
// a server error the module gave up retrying.
func answered(err error) bool {
	if errors.Is(err, dnsprovider.ErrNotFound) {
		return true
	}

	var apiErr *dnsprovider.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode < http.StatusInternalServerError
	}

	return false
}

// uncache removes an Offboarded Zone from the cache; one the cache no
// longer holds, as it was, is gone already.
func uncache(ctx context.Context, s *store.Store, z dns.Zone) error {
	if err := s.RemoveZone(ctx, z); err != nil && !errors.Is(err, store.ErrZoneNotCached) {
		return err
	}

	return nil
}

// zoneOffboarded reports an Offboarding, in the status bar whether or
// not its dialog is still open. One refused closes the dialog, saying
// why; one the Provider failed leaves the dialog open with the error and
// the cache as it was; one made closes it and drops the Zone from the
// pane.
func (m *Model) zoneOffboarded(msg zoneOffboardedMsg) tea.Cmd {
	delete(m.offboarding, msg.zone.ID)

	if msg.resolved {
		m.recordDelegation(nameKey(msg.zone.Name), msg.delegated, msg.resolveErr)
	}

	d := m.offboard
	if d != nil && d.zone.ID != msg.zone.ID {
		d = nil
	}

	switch {
	case msg.refused != nil:
		if d != nil {
			m.offboard = nil
		}

		m.notice = notice{text: msg.refused.Error(), err: true}

		return nil
	case msg.err != nil:
		if d != nil {
			d.busy, d.cancel = false, nil
			d.err = m.offboardError(msg)
		}

		// The status bar says it too, so it is seen once the dialog is
		// closed, or when it was closed meanwhile.
		m.notice = notice{text: m.offboardNotice(msg), err: true}

		return nil
	}

	if d != nil {
		m.offboard = nil
	}

	m.forgetZones([]int64{msg.zone.ID})

	switch {
	case msg.saveErr != nil:
		m.notice = notice{text: msg.zone.Name + " was Offboarded from " + msg.account.Label() + ", but could not be removed from the cache: " +
			msg.saveErr.Error() + ". " + m.reList(), err: true}
	case msg.gone:
		m.notice = notice{text: msg.zone.Name + " is no longer at " + msg.account.Label() + ": it is dropped from the cache."}
	default:
		m.notice = notice{text: msg.zone.Name + " is Offboarded from " + msg.account.Label() + "."}
	}

	return m.loadCache
}

// offboardError says why an Offboarding failed. One whose delete was
// sent says what the read-back found: that the Account still holds the
// Zone, or, when the read failed, that it may be Offboarded all the same.
func (m *Model) offboardError(msg zoneOffboardedMsg) string {
	text := "Could not Offboard " + msg.zone.Name + ": " + strings.TrimSuffix(msg.err.Error(), ".") + "."
	if msg.cancelled {
		text = "Cancelled Offboarding " + msg.zone.Name + "."
	}

	switch {
	case !msg.sent:
		return text + " Nothing was sent to " + msg.account.Label() + "."
	case msg.readBack != nil:
		return text + " It may have been Offboarded all the same, as reading it back failed: " + msg.readBack.Error() + ". " + m.reList()
	case msg.held:
		return text + " " + msg.account.Label() + " still holds it."
	}

	return text
}

// offboardNotice is the status bar's word on a failed Offboarding: what
// may have happened first, as the bar is one line.
func (m *Model) offboardNotice(msg zoneOffboardedMsg) string {
	if msg.sent && msg.readBack != nil {
		return msg.zone.Name + " may have been Offboarded from " + msg.account.Label() + ": the delete failed (" +
			strings.TrimSuffix(msg.err.Error(), ".") + ") and reading it back failed (" + msg.readBack.Error() + "). " + m.reList()
	}

	return m.offboardError(msg)
}

func (m *Model) offboardView() string {
	d := m.offboard
	z := d.zone

	notes := wrapNote(lipgloss.NewStyle(), "Offboard "+z.Name+" from "+d.account.Label()+"?", noteWidth)
	notes = append(notes, "")
	notes = append(notes, wrapNote(lipgloss.NewStyle(), z.Name+" is "+hostingWord(z.State)+" at "+d.account.Label()+
		" and not the Live Zone; both are checked again with "+d.account.Label()+" and the resolver before the delete.", noteWidth)...)
	notes = append(notes, "")
	notes = append(notes, wrapNote(errorStyle, "This stops DNS hosting of "+z.Name+" at "+d.account.Label()+
		" and drops its Records there; it cannot be undone. It leaves the domain's registration untouched.", noteWidth)...)

	if d.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, d.err, noteWidth)...)
	}

	hint := m.keys.keysFor(ActionSelect) + " Offboard · " + m.keys.keysFor(ActionClose) + " close"
	if d.busy {
		hint = "offboarding… " + m.keys.keysFor(ActionClose) + " cancels"
	}

	notes = append(notes, "", hintStyle.Render(hint))

	return dialogView("Offboard a Zone", nil, 0, false, notes)
}
