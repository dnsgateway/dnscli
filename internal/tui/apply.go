// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// changesetPreview shows a Zone's pending Changeset, from which the user
// applies or discards it.
type changesetPreview struct {
	zoneID int64
	err    string
	busy   bool // the Changeset is being applied
	// conflicts describe the Records that changed at the Provider since
	// the Changeset was built, which stopped the last Apply.
	conflicts []string
	// conflicting are those Records, as the Changeset was built from
	// them, which ActionApplyOver rebases the changes of.
	conflicting []conflict
	// outcomes are the changes the last Apply sent, with how each fared,
	// when the Provider failed some of them.
	outcomes []changeOutcome
}

// changeOutcome is how one change of an Apply fared.
type changeOutcome struct {
	change dns.Change
	// err is why the Provider failed the change; nil when it applied it.
	err error
}

// changesApplied reports the result of applying a Changeset.
type changesAppliedMsg struct {
	zoneID int64
	// sent is the Changeset sent to the Provider.
	sent dns.Changeset
	// failed holds, for each change of sent, why the Provider did not
	// apply it; nil for a change it confirmed.
	failed []error
	// err is why nothing was applied, or why the confirmed changes could
	// not be saved to the cache.
	err error
	// saved is false when the Provider confirmed changes that the cache
	// could not take.
	saved bool
	// applied are the confirmed changes saved to the cache, each After
	// as the Provider stored it.
	applied []dns.Change
	// uncached counts the confirmed changes left out of the cache because
	// the Provider returned no ID for their Record.
	uncached int
	// conflicts are the Records the Changeset was built from that changed
	// at the Provider since: nothing was sent.
	conflicts []conflict
	// serial is the Zone's SOA serial the Provider reported, saved with
	// the applied changes; zero when it reported none.
	serial uint32
}

// conflict is a Record a Changeset was built from that changed at the
// Provider since.
type conflict struct {
	// before is the Record as the Changeset was built from it.
	before dns.Record
	// now is the Record as the Provider has it; empty when it is gone.
	now dns.Record
}

// staleConflicts are the stale changes dnsprovider.Apply refused a
// Changeset for, as the conflict view shows them.
func staleConflicts(err *dnsprovider.StaleError) []conflict {
	out := make([]conflict, len(err.Changes))
	for i, c := range err.Changes {
		out[i] = conflict{before: provider.CacheRecord(c.Change.Before), now: provider.CacheRecord(c.Now)}
	}

	return out
}

// describe explains a conflict, naming Records relative to zone.
func (c conflict) describe(zone string) string {
	if c.now.Type == "" {
		return describeRecord(zone, c.before) + " no longer exists"
	}

	return describeRecord(zone, c.before) + " is now " + describeRecord(zone, c.now)
}

// maxPreviewLines bounds how many changes the preview lists.
const maxPreviewLines = 12

// previewChanges opens the preview of the open Zone's pending Changeset,
// from the Records pane.
func (m *Model) previewChanges() tea.Cmd {
	if m.focus != paneRecords {
		m.notice = notice{text: "Pending changes are previewed from the Records pane (" + m.keys.keysFor(ActionPreviewChanges) + " there)."}

		return nil
	}

	if m.open == nil {
		m.notice = notice{text: "Open a Zone first.", err: true}

		return nil
	}

	if m.pendingOf(m.open.ID) == nil {
		m.notice = notice{text: "No pending changes for " + m.open.Name + "."}

		return nil
	}

	m.preview = &changesetPreview{zoneID: m.open.ID}

	return nil
}

// previewKey handles a key while the Changeset preview is open.
func (m *Model) previewKey(msg tea.KeyMsg) tea.Cmd {
	p := m.preview
	a := m.keys.action(msg.String())

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if p.busy {
		return nil
	}

	switch a {
	case ActionClose:
		m.preview = nil
	case ActionDiscardChanges:
		n := len(m.changesets[p.zoneID].Changes)
		delete(m.changesets, p.zoneID)
		m.preview = nil
		m.rebuildRecordRows()
		m.notice = notice{text: fmt.Sprintf("Discarded %s for %s.", plural(n, "pending change"), m.zoneByID[p.zoneID].Name)}
	case ActionSelect:
		return m.applyChanges()
	case ActionApplyOver:
		if len(p.conflicting) > 0 {
			return m.applyOver()
		}
	}

	return nil
}

// applyOver rebases the changes built from the Records that conflicted
// onto the Records as the conflict's Refresh cached them, and applies the
// Changeset. A modification or deletion is then of the Record as it is
// now. Of a Record that is gone, a deletion is dropped and a modification
// becomes an addition. Every other change is left as it is.
func (m *Model) applyOver() tea.Cmd {
	p := m.preview

	if !m.validatedZone(p.zoneID) || m.open == nil || m.open.ID != p.zoneID || m.recordsLoading {
		p.err = "The Zone is being Refreshed: apply over the current values once it is validated."

		return nil
	}

	cs := m.pendingOf(p.zoneID)
	if cs == nil {
		return nil
	}

	gone := map[string]bool{}
	for _, c := range p.conflicting {
		gone[c.before.ProviderID] = true
	}

	current := map[string]dns.Record{}
	for _, r := range m.records {
		current[r.ProviderID] = r
	}

	var rebased []dns.Change

	for _, c := range cs.Changes {
		if c.Kind == dns.ChangeAdd || !gone[c.Before.ProviderID] {
			rebased = append(rebased, c)

			continue
		}

		now, ok := current[c.Before.ProviderID]

		switch {
		case ok:
			c.Before = now
			if c.Kind == dns.ChangeModify && sameRecord(c.Before, c.After) {
				continue // already as the change makes it
			}
		case c.Kind == dns.ChangeDelete:
			continue
		default:
			c.Kind, c.Before = dns.ChangeAdd, dns.Record{}
			c.After.ProviderID = ""
		}

		rebased = append(rebased, c)
	}

	cs.Changes = rebased
	m.rebuildRecordRows()

	if len(rebased) == 0 {
		delete(m.changesets, p.zoneID)
		m.preview = nil
		m.notice = notice{text: "Nothing left to apply to " + m.zoneByID[p.zoneID].Name + ": the Records are already as the changes made them."}

		return nil
	}

	return m.applyChanges()
}

// applyChanges sends the previewed Changeset to the Provider, past the
// write gate.
func (m *Model) applyChanges() tea.Cmd {
	p := m.preview
	p.err, p.conflicts, p.conflicting, p.outcomes = "", nil, nil, nil

	z, cmd, ok := m.writeGate()
	if !ok || z.ID != p.zoneID {
		p.err = m.notice.text

		return cmd
	}

	pending := m.pendingOf(z.ID)
	a, found := m.account(z.AccountID)

	if pending == nil || !found {
		m.preview = nil

		return nil
	}

	cs := dns.Changeset{Zone: z, Changes: slices.Clone(pending.Changes)}

	if m.applying == nil {
		m.applying = map[int64]bool{}
	}

	m.applying[z.ID] = true
	p.busy = true

	ctx, s, key, providers := m.ctx, m.deps.Store, m.key, m.providers

	return func() tea.Msg {
		return apply(ctx, s, key, providers, a, cs)
	}
}

// apply sends a Changeset through dnsprovider.Apply, which first
// re-checks the Records it was built from and refuses it when any
// changed, and saves the changes the Provider confirmed to the cache,
// from the Records it returned.
//
// It stores the Zone's new serial when the Provider reports it. Else the
// model polls the Zone's nameservers for it afterwards (serialPoll).
func apply(ctx context.Context, s *store.Store, key *vault.Key, providers *provider.Opener, a dns.Account, cs dns.Changeset) changesAppliedMsg {
	msg := changesAppliedMsg{zoneID: cs.Zone.ID, sent: cs}

	p, _, err := accountProvider(ctx, s, key, providers, a)
	if err != nil {
		msg.err = err

		return msg
	}

	res, err := dnsprovider.Apply(ctx, p, provider.ModuleChangeset(cs))

	if stale, ok := errors.AsType[*dnsprovider.StaleError](err); ok {
		msg.conflicts = staleConflicts(stale)

		return msg
	}

	if err == nil && len(res.Results) != len(cs.Changes) {
		err = fmt.Errorf("the Provider reported %d results for %d changes", len(res.Results), len(cs.Changes))
	}

	if err != nil {
		msg.err = err

		return msg
	}

	msg.failed = make([]error, len(cs.Changes))

	var confirmed []dns.Change

	for i, r := range res.Results {
		if r.Err != nil {
			msg.failed[i] = r.Err

			continue
		}

		c := cs.Changes[i]
		if c.Kind != dns.ChangeDelete {
			c.After = storedRecord(c, provider.CacheRecord(r.Record))
		}

		// A Record without its Provider ID cannot be told apart from
		// other such Records: a Refresh must cache it.
		if (c.Kind != dns.ChangeAdd && c.Before.ProviderID == "") || (c.Kind != dns.ChangeDelete && c.After.ProviderID == "") {
			msg.uncached++

			continue
		}

		confirmed = append(confirmed, c)
	}

	if err := s.SaveApply(ctx, cs.Zone, confirmed, res.Serial); err != nil {
		msg.err = err

		return msg
	}

	msg.serial = res.Serial

	msg.saved, msg.applied = true, confirmed

	return msg
}

// tenantUse shows the Zone's pending Changeset again, for the user to
// confirm its Apply within the Tenant picked: it is not sent unconfirmed,
// as the user confirmed it before knowing which Tenant it is sent to. A
// Changeset applied or dropped meanwhile, a Zone no longer open, or
// another dialog open, show nothing.
func (msg changesAppliedMsg) tenantUse(m *Model) (int64, func() tea.Cmd, error) {
	z, ok := m.zoneByID[msg.zoneID]
	if !ok {
		return 0, nil, nil
	}

	return z.AccountID, func() tea.Cmd {
		if m.open != nil && m.open.ID == z.ID && m.pendingOf(z.ID) != nil && !m.dialogOpen() && m.tenantPick == nil {
			m.preview = &changesetPreview{zoneID: z.ID}
		}

		return nil
	}, msg.err
}

// storedRecord is the Record as the Provider stored it for a confirmed
// addition or modification. A Provider that returned no Record stored the
// one sent, under its old ID for a modification.
func storedRecord(c dns.Change, stored dns.Record) dns.Record {
	if stored.Type != "" {
		return stored
	}

	r := c.After
	if c.Kind == dns.ChangeModify {
		r.ProviderID = c.Before.ProviderID
	}

	return r
}

// changesApplied takes the confirmed changes out of the pending
// Changeset, keeping the ones that failed, and shows the cache as updated.
// When the cache could not take what the Provider confirmed, the Zone is
// no longer validated: it must be Refreshed before the next edit.
func (m *Model) changesApplied(msg changesAppliedMsg) tea.Cmd {
	delete(m.applying, msg.zoneID)

	z, known := m.zoneByID[msg.zoneID]
	if !known {
		return nil // forgotten meanwhile, such as with its Account
	}

	p := m.preview
	if p != nil && p.zoneID == msg.zoneID {
		p.busy = false
	}

	if msg.conflicts != nil {
		return m.applyConflicted(z, msg.conflicts)
	}

	if msg.failed == nil {
		text := "Could not apply the changes to " + z.Name + ": " + msg.err.Error()
		m.notice = notice{text: text, err: true}

		if p != nil && p.zoneID == msg.zoneID {
			p.err = text

			// Nothing was sent: the picker asks for the Tenant in its
			// place, and the Changeset stays pending.
			if errors.As(msg.err, new(*provider.TenantChoiceError)) && m.tenantPick == nil {
				m.preview = nil
			}
		}

		return nil
	}

	// The Records pane shows what was saved to the cache straight away,
	// in the same step that takes the changes out of the Changeset.
	if m.open != nil && m.open.ID == msg.zoneID {
		m.records = applyToRecords(m.records, msg.applied)
	}

	var poll tea.Cmd

	switch {
	case msg.saved && msg.serial != 0:
		z.Serial = msg.serial
		m.updateZone(z)
	case msg.saved && msg.uncached == 0:
		// The Provider reported no serial: its nameservers are asked for
		// it in the background, once they may have taken the changes.
		poll = m.pollSerial(msg.sent.Zone, slices.Contains(msg.failed, nil))
	}

	if r, ok := m.refreshes[msg.zoneID]; ok {
		switch {
		case msg.saved && msg.uncached == 0:
			// The cache holds what the Provider confirmed: still validated.
			r.validated = true
		case msg.saved:
			*r = zoneRefresh{err: errors.New("the Provider did not return the IDs of the Records it stored")}
		default:
			*r = zoneRefresh{err: fmt.Errorf("the applied changes could not be cached: %w", msg.err)}
		}
	}

	m.dropApplied(msg)
	m.rebuildRecordRows()

	failed := 0

	var first error

	for _, err := range msg.failed {
		if err != nil {
			failed++

			if first == nil {
				first = err
			}
		}
	}

	applied := len(msg.sent.Changes) - failed

	text := fmt.Sprintf("Applied %s to %s.", plural(applied, "change"), z.Name)
	if failed > 0 {
		text = fmt.Sprintf("Applied %d of %d changes to %s; %d failed and are still pending: %v.",
			applied, len(msg.sent.Changes), z.Name, failed, first)
	}

	switch {
	case !msg.saved:
		text += fmt.Sprintf(" Refresh (%s) needed: they could not be cached: %v", m.recordsKeys(ActionRefresh), msg.err)
	case msg.uncached > 0:
		text += fmt.Sprintf(" Refresh (%s) needed: the Provider did not return the IDs of %d to cache.",
			m.recordsKeys(ActionRefresh), msg.uncached)
	}

	m.notice = notice{text: text, err: failed > 0 || !msg.saved || msg.uncached > 0}

	if p != nil && p.zoneID == msg.zoneID && m.pendingOf(msg.zoneID) == nil {
		m.preview = nil
	}

	if p != nil && m.preview != nil && failed > 0 {
		p.outcomes = make([]changeOutcome, len(msg.sent.Changes))
		for i, c := range msg.sent.Changes {
			p.outcomes[i] = changeOutcome{change: c, err: msg.failed[i]}
		}

		// The outcomes say which changes failed and why.
		if !msg.saved || msg.uncached > 0 {
			p.err = m.notice.text
		}
	}

	return poll
}

// applyConflicted reports an Apply that was not sent because Records it
// was built from changed at the Provider, and Refreshes the Zone. The
// changes stay pending for the user to review.
func (m *Model) applyConflicted(z dns.Zone, cs []conflict) tea.Cmd {
	described := make([]string, len(cs))
	for i, c := range cs {
		described[i] = c.describe(z.Name)
	}

	verb := "have"
	if len(cs) == 1 {
		verb = "has"
	}

	text := fmt.Sprintf("Nothing was applied to %s: %s %s changed at the Provider since the last Refresh. Refreshing it; review the pending changes.",
		z.Name, plural(len(cs), "Record"), verb)
	m.notice = notice{text: text + " " + strings.Join(described, "; ") + ".", err: true}

	if p := m.preview; p != nil && p.zoneID == z.ID {
		p.err, p.conflicts, p.conflicting = text, described, cs
	}

	return m.refreshZone(z)
}

// applyToRecords returns Records with the applied changes made to them as
// SaveApply makes them to the cache, in the order the Store lists them.
func applyToRecords(records []dns.Record, applied []dns.Change) []dns.Record {
	out := slices.Clone(records)

	for _, c := range applied {
		if c.Kind != dns.ChangeAdd {
			out = slices.DeleteFunc(out, func(r dns.Record) bool { return r.ProviderID == c.Before.ProviderID })
		}

		if c.Kind != dns.ChangeDelete {
			out = append(out, c.After)
		}
	}

	slices.SortFunc(out, func(a, b dns.Record) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Type, b.Type), strings.Compare(a.Value, b.Value))
	})

	return out
}

// dropApplied takes the changes the Provider confirmed out of the pending
// Changeset. Edits are refused while an Apply is in flight, so the pending
// Changeset still starts with the changes that were sent.
func (m *Model) dropApplied(msg changesAppliedMsg) {
	cs := m.changesets[msg.zoneID]
	if cs == nil {
		return
	}

	var kept []dns.Change

	for i, c := range cs.Changes {
		if i >= len(msg.failed) || msg.failed[i] != nil {
			kept = append(kept, c)
		}
	}

	cs.Changes = kept
}

// plural says how many of a noun, such as "1 change" or "3 pending changes".
func plural(n int, noun string) string {
	if n != 1 {
		noun += "s"
	}

	return strconv.Itoa(n) + " " + noun
}

func (m *Model) previewView() string {
	p := m.preview
	z := m.zoneByID[p.zoneID]

	var lines []string

	if len(p.outcomes) > 0 {
		lines = append(lines, outcomeLines(z.Name, p.outcomes)...)
		lines = append(lines, "")
	}

	if cs := m.pendingOf(p.zoneID); cs != nil {
		lines = append(lines, "Applying sends "+plural(len(cs.Changes), "pending change")+" to the Provider:", "")

		for i, c := range cs.Changes {
			if i == maxPreviewLines {
				lines = append(lines, hintStyle.Render(fmt.Sprintf("… and %d more", len(cs.Changes)-i)))

				break
			}

			lines = append(lines, changeStyle(c.Kind).Render(describeChange(z.Name, c)))
		}
	}

	if p.err != "" {
		lines = append(lines, "")
		lines = append(lines, wrapNote(errorStyle, p.err, noteWidth)...)
	}

	for _, c := range p.conflicts {
		lines = append(lines, errorStyle.Render("  "+c))
	}

	if p.busy {
		lines = append(lines, "", hintStyle.Render("applying…"))
	}

	lines = append(lines, "")

	if len(p.conflicting) > 0 && !p.busy {
		lines = append(lines, hintStyle.Render(m.keys.keysFor(ActionApplyOver)+" apply over the current values"))
	}

	lines = append(lines, hintStyle.Render(m.keys.keysFor(ActionSelect)+" apply · "+
		m.keys.keysFor(ActionDiscardChanges)+" discard · "+m.keys.keysFor(ActionClose)+" close"))

	return dialogView("Changes to "+z.Name, nil, -1, false, lines)
}

// outcomeLines lists how each change of an Apply fared: ✓ for a change
// the Provider applied, ✗ for one it failed, followed by why.
func outcomeLines(zone string, outcomes []changeOutcome) []string {
	applied := 0

	for _, o := range outcomes {
		if o.err == nil {
			applied++
		}
	}

	lines := []string{fmt.Sprintf("The Provider applied %d of %d changes:", applied, len(outcomes))}

	for i, o := range outcomes {
		if i == maxPreviewLines {
			lines = append(lines, hintStyle.Render(fmt.Sprintf("… and %d more", len(outcomes)-i)))

			break
		}

		if o.err == nil {
			lines = append(lines, hintCurrentStyle.Render("✓ "+describeChange(zone, o.change)))

			continue
		}

		lines = append(lines, errorStyle.Render("✗ "+describeChange(zone, o.change)))
		for _, l := range wrapNote(errorStyle, o.err.Error(), noteWidth-4) {
			lines = append(lines, "    "+l)
		}
	}

	return lines
}
