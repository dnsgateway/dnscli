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
	// serial is the Zone's SOA serial saved with the applied changes;
	// zero when the cached one was kept.
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

// conflicts returns the Records the changes were built from that the
// Provider no longer has as they were. An addition is built from no
// Record, so it never conflicts.
func conflicts(changes []dns.Change, live []dns.Record) []conflict {
	byID := map[string]dns.Record{}
	for _, r := range live {
		byID[r.ProviderID] = r
	}

	var out []conflict

	for _, c := range changes {
		if c.Kind == dns.ChangeAdd {
			continue
		}

		now, ok := byID[c.Before.ProviderID]
		if !ok || !sameRecord(c.Before, now) {
			out = append(out, conflict{before: c.Before, now: now})
		}
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

// previewChanges opens the preview of the open Zone's pending Changeset.
func (m *Model) previewChanges() tea.Cmd {
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
	}

	return nil
}

// applyChanges sends the previewed Changeset to the Provider, past the
// write gate.
func (m *Model) applyChanges() tea.Cmd {
	p := m.preview
	p.err, p.conflicts, p.outcomes = "", nil, nil

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

	ctx, s, key, newProvider := m.ctx, m.deps.Store, m.key, m.deps.Providers[a.Provider]
	q := m.serialQueryFor(z)

	return func() tea.Msg {
		return apply(ctx, s, key, newProvider, a, cs, q)
	}
}

// apply re-checks the Records a Changeset was built from against the
// Provider and, when none changed, sends the Changeset and saves the
// changes the Provider confirmed to the cache, from the Records it
// returned.
//
// It stores the Zone's new serial when the Provider reports it. Else, once
// any change applied, q asks the Zone's nameservers, whose serial is
// stored only when it is newer than the cached one: they may not have
// taken the changes yet.
func apply(ctx context.Context, s *store.Store, key *vault.Key, newProvider provider.Constructor, a dns.Account, cs dns.Changeset, q serialQuery) changesAppliedMsg {
	msg := changesAppliedMsg{zoneID: cs.Zone.ID, sent: cs}

	p, err := accountProvider(ctx, s, key, newProvider, a)
	if err != nil {
		msg.err = err

		return msg
	}

	live, err := p.Records(ctx, cs.Zone)
	if err != nil {
		msg.err = fmt.Errorf("re-check the Records: %w", err)

		return msg
	}

	if msg.conflicts = conflicts(cs.Changes, live.Records); msg.conflicts != nil {
		return msg
	}

	res, err := p.Apply(ctx, cs)
	if err == nil && len(res.Results) != len(cs.Changes) {
		err = fmt.Errorf("the Provider reported %d results for %d changes", len(res.Results), len(cs.Changes))
	}

	if err != nil {
		msg.err = err

		return msg
	}

	msg.failed = make([]error, len(cs.Changes))

	var (
		confirmed []dns.Change
		applied   bool
	)

	for i, r := range res.Results {
		if r.Err != nil {
			msg.failed[i] = r.Err

			continue
		}

		applied = true

		c := cs.Changes[i]
		if c.Kind != dns.ChangeDelete {
			c.After = storedRecord(c, r.Record)
		}

		// A Record without its Provider ID cannot be told apart from
		// other such Records: a Refresh must cache it.
		if (c.Kind != dns.ChangeAdd && c.Before.ProviderID == "") || (c.Kind != dns.ChangeDelete && c.After.ProviderID == "") {
			msg.uncached++

			continue
		}

		confirmed = append(confirmed, c)
	}

	if res.Serial == 0 && applied {
		if serial := q.serial(ctx, cs.Zone.Name); serialNewer(serial, cs.Zone.Serial) {
			res.Serial = serial
		}
	}

	if err := s.SaveApply(ctx, cs.Zone, confirmed, res.Serial); err != nil {
		msg.err = err

		return msg
	}

	msg.serial = res.Serial

	msg.saved, msg.applied = true, confirmed

	return msg
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
		}

		return nil
	}

	// The Records pane shows what was saved to the cache straight away,
	// in the same step that takes the changes out of the Changeset.
	if m.open != nil && m.open.ID == msg.zoneID {
		m.records = applyToRecords(m.records, msg.applied)
	}

	if msg.saved && msg.serial != 0 {
		z.Serial = msg.serial
		m.updateZone(z)
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
		text += fmt.Sprintf(" Refresh (%s) needed: they could not be cached: %v", m.keys.keysFor(ActionRefresh), msg.err)
	case msg.uncached > 0:
		text += fmt.Sprintf(" Refresh (%s) needed: the Provider did not return the IDs of %d to cache.",
			m.keys.keysFor(ActionRefresh), msg.uncached)
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

	return nil
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
		p.err, p.conflicts = text, described
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

	lines = append(lines, "", hintStyle.Render(m.keys.keysFor(ActionSelect)+" apply · "+
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
