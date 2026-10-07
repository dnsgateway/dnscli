package tui

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// recordRow is one row of the Records pane: a cached Record, with its
// pending change applied, or a Record a pending change adds.
type recordRow struct {
	// rec is the Record as the row shows it: the After of an addition or
	// modification, else the cached Record.
	rec dns.Record
	// base is the cached Record the row comes from; empty for an addition.
	base dns.Record
	// change is the row's pending change: its index in the Zone's
	// Changeset, or -1 when it has none.
	change int
	// kind is the kind of the pending change; zero when there is none.
	kind dns.ChangeKind
}

// recordRows lists the open Zone's cached Records with its pending
// Changeset laid over them. A modification or deletion shows on the
// Record it was built from. Additions follow the cached Records, and so
// does a change whose Record is no longer cached, so no pending change is
// ever hidden.
func (m *Model) recordRows() []recordRow {
	var cs *dns.Changeset
	if m.open != nil {
		cs = m.pendingOf(m.open.ID)
	}

	byID := map[string]int{}
	shown := map[int]bool{}

	if cs != nil {
		for i, c := range cs.Changes {
			if c.Kind != dns.ChangeAdd && c.Before.ProviderID != "" {
				byID[c.Before.ProviderID] = i
			}
		}
	}

	rows := make([]recordRow, 0, len(m.records))

	for _, r := range m.records {
		row := recordRow{rec: r, base: r, change: -1}

		if i, ok := byID[r.ProviderID]; ok && r.ProviderID != "" {
			row.change, row.kind = i, cs.Changes[i].Kind
			shown[i] = true

			if row.kind == dns.ChangeModify {
				row.rec = cs.Changes[i].After
			}
		}

		rows = append(rows, row)
	}

	if cs != nil {
		for i, c := range cs.Changes {
			if shown[i] {
				continue
			}

			row := recordRow{rec: c.After, base: c.Before, change: i, kind: c.Kind}
			if c.Kind == dns.ChangeDelete {
				row.rec = c.Before
			}

			rows = append(rows, row)
		}
	}

	return rows
}

// writeGate reports whether the open Zone may be edited: it must be a
// Validated Zone in an unsealed session (ADR-0001), with no Apply in
// flight. When it may not, the status bar says why, and a sealed session
// is offered Unseal, after which the Zone is Refreshed so that it can be
// validated. Every action that changes a Changeset or applies it passes
// through here.
func (m *Model) writeGate() (dns.Zone, tea.Cmd, bool) {
	if m.open == nil {
		m.notice = notice{text: "Open a Zone first.", err: true}

		return dns.Zone{}, nil, false
	}

	z := *m.open

	switch {
	case m.sealed():
		m.notice = notice{text: "The session is sealed and read-only: Unseal to edit " + z.Name + ".", err: true}

		return z, m.withSecrets(func() tea.Cmd {
			m.notice = notice{} // no longer sealed

			return m.refreshZone(z)
		}), false
	case m.applying[z.ID]:
		m.notice = notice{text: "The pending changes for " + z.Name + " are being applied…", err: true}

		return z, nil, false
	case !m.validatedZone(z.ID):
		m.notice = notice{text: fmt.Sprintf("%s is a Stale Zone: it can be edited once a Refresh validates it (%s Refreshes).",
			z.Name, m.keys.keysFor(ActionRefresh)), err: true}

		return z, nil, false
	}

	return z, nil, true
}

// pendingOf returns a Zone's pending Changeset; nil when it has none.
func (m *Model) pendingOf(zoneID int64) *dns.Changeset {
	cs := m.changesets[zoneID]
	if cs == nil || len(cs.Changes) == 0 {
		return nil
	}

	return cs
}

// changesetFor returns a Zone's pending Changeset, starting an empty one
// when it has none.
func (m *Model) changesetFor(z dns.Zone) *dns.Changeset {
	if m.changesets == nil {
		m.changesets = map[int64]*dns.Changeset{}
	}

	cs := m.changesets[z.ID]
	if cs == nil {
		cs = &dns.Changeset{Zone: z}
		m.changesets[z.ID] = cs
	}

	return cs
}

// highlightedRecord returns the row under the Records pane cursor.
func (m *Model) highlightedRecord() (recordRow, bool) {
	if m.focus != paneRecords || len(m.visibleRecords) == 0 {
		return recordRow{}, false
	}

	return m.visibleRecords[m.cursor[paneRecords]], true
}

// recordAction runs a Record action. They act in the Records pane, on the
// open Zone, and only past the write gate.
func (m *Model) recordAction(a Action) tea.Cmd {
	if m.focus != paneRecords {
		m.notice = notice{text: fmt.Sprintf("Records are edited in the Records pane (%s).", m.keys.keysFor(ActionNextPane)), err: true}

		return nil
	}

	z, cmd, ok := m.writeGate()
	if !ok {
		return cmd
	}

	if a == ActionAddRecord {
		m.recordForm = newRecordForm(z, nil)

		return nil
	}

	row, ok := m.highlightedRecord()
	if !ok {
		m.notice = notice{text: "Highlight a Record first.", err: true}

		return nil
	}

	// Without its Provider ID a cached Record cannot be told apart from
	// another, nor changed at the Provider.
	if row.kind != dns.ChangeAdd && row.base.ProviderID == "" {
		m.notice = notice{text: fmt.Sprintf("This %s Record has no Provider ID, so dnscli cannot change it.", row.rec.Type), err: true}

		return nil
	}

	if a == ActionDeleteRecord {
		m.stageDelete(z, row)

		return nil
	}

	if !dns.Editable(row.rec.Type) {
		m.notice = notice{text: fmt.Sprintf("dnscli cannot edit %s Records; %s deletes one.", row.rec.Type, m.keys.keysFor(ActionDeleteRecord)), err: true}

		return nil
	}

	m.recordForm = newRecordForm(z, &row)

	return nil
}

// stageAdd adds a Record to a Zone's pending Changeset.
func (m *Model) stageAdd(z dns.Zone, r dns.Record) {
	cs := m.changesetFor(z)
	cs.Changes = append(cs.Changes, dns.Change{Kind: dns.ChangeAdd, After: r})
	m.refilter(paneRecords)
}

// stageEdit changes the Record a row shows to r. Editing an addition
// changes what it adds. Editing a cached Record modifies it, from the
// Record as cached, and editing it back to that drops the change.
func (m *Model) stageEdit(z dns.Zone, row recordRow, r dns.Record) {
	cs := m.changesetFor(z)

	switch {
	case row.kind == dns.ChangeAdd:
		cs.Changes[row.change].After = r
	case row.change >= 0:
		c := &cs.Changes[row.change]
		c.Kind, c.After = dns.ChangeModify, r
	default:
		cs.Changes = append(cs.Changes, dns.Change{Kind: dns.ChangeModify, Before: row.base, After: r})
		row.change = len(cs.Changes) - 1
	}

	if c := cs.Changes[row.change]; c.Kind == dns.ChangeModify && sameRecord(c.Before, c.After) {
		cs.Changes = slices.Delete(cs.Changes, row.change, row.change+1)
	}

	m.refilter(paneRecords)
}

// stageDelete marks a row's Record for deletion. On a row that has a
// pending change it takes the change back instead: an addition is
// dropped, a deletion undone, and a modification becomes a deletion.
func (m *Model) stageDelete(z dns.Zone, row recordRow) {
	cs := m.changesetFor(z)

	switch row.kind {
	case dns.ChangeAdd, dns.ChangeDelete:
		cs.Changes = slices.Delete(cs.Changes, row.change, row.change+1)
	case dns.ChangeModify:
		// Like the modification, the deletion is of the Record as the
		// change was built from it, whatever the cache holds now.
		c := &cs.Changes[row.change]
		c.Kind, c.After = dns.ChangeDelete, dns.Record{}
	default:
		cs.Changes = append(cs.Changes, dns.Change{Kind: dns.ChangeDelete, Before: row.base})
	}

	m.refilter(paneRecords)
}

// sameRecord reports whether two Records are the same Record with the same
// data. Names compare as DNS names do: in any case, with or without a
// trailing dot.
func sameRecord(a, b dns.Record) bool {
	return a.ProviderID == b.ProviderID && a.Type == b.Type &&
		strings.EqualFold(strings.TrimSuffix(a.Name, "."), strings.TrimSuffix(b.Name, ".")) &&
		a.Value == b.Value && a.TTL == b.TTL && maps.Equal(a.Extras, b.Extras)
}

// changeMark is the one-column mark the Records pane draws for a pending change.
func changeMark(k dns.ChangeKind) string {
	switch k {
	case dns.ChangeAdd:
		return "+"
	case dns.ChangeModify:
		return "~"
	case dns.ChangeDelete:
		return "-"
	}

	return " "
}

// describeChange is one line of the Changeset preview.
func describeChange(zone string, c dns.Change) string {
	rec := func(r dns.Record) string {
		return strings.Join([]string{dns.RelativeName(zone, r.Name), r.Type, r.Value, "TTL " + strconv.FormatUint(uint64(r.TTL), 10)}, " ")
	}

	switch c.Kind {
	case dns.ChangeAdd:
		return "+ " + rec(c.After)
	case dns.ChangeDelete:
		return "- " + rec(c.Before)
	}

	return "~ " + rec(c.Before) + " → " + rec(c.After)
}
