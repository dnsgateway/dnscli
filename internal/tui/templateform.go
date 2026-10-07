package tui

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/template"
)

// templateTTL is the TTL of the Records a Template adds, where the
// Provider has no automatic TTL.
const templateTTL = 3600

// templateForm picks a built-in Template for the open Zone and asks for
// its parameters. Its first field is the Template; the parameters of the
// picked Template follow.
type templateForm struct {
	zone      dns.Zone
	templates []template.Template
	pick      int
	field     int
	// values are the parameter values typed so far, by Template name, so
	// that picking another Template and back keeps them.
	values map[string]template.Values
	err    string
	// conflicts describe the pending changes the Template would change,
	// which stopped it.
	conflicts []string
	// warning says what the Template would weaken, such as the Zone's
	// DMARC policy; a second Enter stages it anyway.
	warning string
}

// openTemplateForm opens the Template dialog for the open Zone, past the
// write gate.
func (m *Model) openTemplateForm() tea.Cmd {
	if m.focus != paneRecords {
		m.notice = notice{text: fmt.Sprintf("Templates are applied in the Records pane (%s).", m.keys.keysFor(ActionNextPane)), err: true}

		return nil
	}

	z, cmd, ok := m.writeGate()
	if !ok {
		return cmd
	}

	f := &templateForm{zone: z, templates: template.Builtin(), values: map[string]template.Values{}}
	_, current := m.pendingRecords()

	for _, t := range f.templates {
		f.values[t.Name] = t.Prefill(z.Name, current)
	}

	m.templateForm = f

	return nil
}

func (f *templateForm) template() template.Template {
	return f.templates[f.pick]
}

// param returns the parameter of the focused field; false on the
// Template field.
func (f *templateForm) param() (template.Param, bool) {
	if f.field == 0 {
		return template.Param{}, false
	}

	return f.template().Params[f.field-1], true
}

// templateFormKey handles a key while the Template dialog is open.
func (m *Model) templateFormKey(msg tea.KeyMsg) tea.Cmd {
	f := m.templateForm
	a := m.keys.action(msg.String())
	fields := 1 + len(f.template().Params)
	values := f.values[f.template().Name]

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if p, ok := f.param(); ok && p.Choices == nil {
		v := values[p.Name]
		if m.keys.typeInto(&v, msg) {
			values[p.Name] = v
			f.warning = ""

			return nil
		}
	}

	switch a {
	case ActionClose:
		m.templateForm = nil
	case ActionNextPane:
		f.field = (f.field + 1) % fields
	case ActionPrevPane:
		f.field = (f.field + fields - 1) % fields
	case ActionUp, ActionDown:
		step := 1
		if a == ActionUp {
			step = -1
		}

		p, ok := f.param()

		switch {
		case !ok:
			n := len(f.templates)
			f.pick = (f.pick + step + n) % n
			f.err, f.conflicts, f.warning = "", nil, ""
		case p.Choices != nil:
			n := len(p.Choices)
			i := max(slices.Index(p.Choices, values[p.Name]), 0)
			values[p.Name] = p.Choices[(i+step+n)%n]
			f.warning = ""
		}
	case ActionSelect:
		if f.field < fields-1 {
			f.field++

			return nil
		}

		return m.submitTemplateForm()
	}

	return nil
}

// submitTemplateForm stages the changes the picked Template makes to the
// open Zone, as its pending Changeset would leave it, into that
// Changeset. The Template merges with the pending changes but never
// overrides one: if it would change a Record the user has a pending change
// for, or bring back one staged for deletion, it stages nothing and lists
// those changes. If it would weaken something, such as the Zone's DMARC
// policy, it says so first and stages on a second Enter.
func (m *Model) submitTemplateForm() tea.Cmd {
	f := m.templateForm
	f.err, f.conflicts = "", nil

	z, cmd, ok := m.writeGate()
	if !ok || z.ID != f.zone.ID {
		f.err = m.notice.text

		return cmd
	}

	if m.recordsLoading {
		f.err = "The Records of " + z.Name + " are still loading."

		return nil
	}

	t := f.template()

	rows, current := m.pendingRecords()

	changes, err := t.Changes(z.Name, current, f.values[t.Name], m.templateTTL(z.ID))
	if err != nil {
		var pe *template.ParamError
		if errors.As(err, &pe) {
			f.field = 1 + slices.IndexFunc(t.Params, func(p template.Param) bool { return p.Name == pe.Param })
		}

		f.err = capitalise(err.Error()) + "."

		return nil
	}

	if len(changes) == 0 {
		m.templateForm = nil
		m.notice = notice{text: fmt.Sprintf("%s already has the Records of %s: nothing to change.", z.Name, t.Name)}

		return nil
	}

	// Each modification and deletion is of a row; find it, and refuse to
	// touch a row that has a pending change.
	of := make([]recordRow, len(changes))
	used := map[int]bool{}

	for i, c := range changes {
		if c.Kind == dns.ChangeAdd {
			for _, row := range m.rows {
				if row.kind == dns.ChangeDelete && template.SameData(row.rec, c.After) {
					f.conflicts = append(f.conflicts, describeRecord(z.Name, row.rec)+" (pending deletion)")
				}
			}

			continue
		}

		j := -1

		for k, r := range rows {
			if !used[k] && sameRecord(r.rec, c.Before) {
				j = k

				break
			}
		}

		if j < 0 {
			f.err = "The Records changed while the Template was open: try again."

			return nil
		}

		used[j] = true
		of[i] = rows[j]

		switch {
		case rows[j].change >= 0:
			f.conflicts = append(f.conflicts, describeRecord(z.Name, rows[j].rec))
		case rows[j].base.ProviderID == "":
			// Without its Provider ID, it cannot be changed at the Provider.
			f.err = fmt.Sprintf("Nothing was staged: %s would change %s, which has no Provider ID, so dnscli cannot change it.",
				t.Name, describeRecord(z.Name, rows[j].rec))

			return nil
		}
	}

	if len(f.conflicts) > 0 {
		f.err = fmt.Sprintf("Nothing was staged: %s would change Records you have pending changes for. "+
			"Discard them (%s, then %s) or make them what the Template wants, and apply it again:",
			t.Name, m.keys.keysFor(ActionPreviewChanges), m.keys.keysFor(ActionDiscardChanges))

		return nil
	}

	if w := t.Warning(z.Name, current, f.values[t.Name]); w != "" && w != f.warning {
		f.warning = w

		return nil
	}

	for i, c := range changes {
		switch c.Kind {
		case dns.ChangeAdd:
			m.stageAdd(z, c.After)
		case dns.ChangeModify:
			m.stageEdit(z, of[i], unproxied(c.After))
		case dns.ChangeDelete:
			m.stageDelete(z, of[i])
		}
	}

	// The status bar now counts the pending changes.
	m.templateForm = nil

	return nil
}

// pendingRecords are the open Zone's Records as they will be once its
// pending Changeset is applied, with the rows they show on.
func (m *Model) pendingRecords() ([]recordRow, []dns.Record) {
	var (
		rows    []recordRow
		current []dns.Record
	)

	for _, row := range m.rows {
		if row.kind != dns.ChangeDelete {
			rows = append(rows, row)
			current = append(current, row.rec)
		}
	}

	return rows, current
}

// templateTTL is the TTL of the Records a Template adds to a Zone: the
// automatic TTL where its Provider has one, else an hour, kept inside the
// Provider's TTL range.
func (m *Model) templateTTL(zoneID int64) uint32 {
	caps := m.refreshOf(zoneID).caps
	if caps.autoTTL {
		return dns.TTLAuto
	}

	r := caps.ttl
	if r == (dns.TTLRange{}) {
		r = dns.DefaultTTLRange
	}

	return min(max(templateTTL, r.Min), r.Max)
}

// unproxied is a Record a Template modifies with its proxy flag turned
// off: a Template's Records, such as a DKIM CNAME, are never proxied.
func unproxied(r dns.Record) dns.Record {
	if _, ok := r.Extras[proxiedExtra]; ok {
		r.Extras = maps.Clone(r.Extras)
		r.Extras[proxiedExtra] = "false"
	}

	return r
}

func (m *Model) templateFormView() string {
	f := m.templateForm
	t := f.template()
	values := f.values[t.Name]

	fields := []dialogField{{label: "Template", value: t.Name, choice: true}}
	for _, p := range t.Params {
		fields = append(fields, dialogField{label: p.Label, value: values[p.Name], choice: p.Choices != nil})
	}

	notes := wrapNote(hintStyle, t.Summary, noteWidth)

	if p, ok := f.param(); ok && p.Hint != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(hintStyle, p.Hint, noteWidth)...)
	}

	if f.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, f.err, noteWidth)...)
	}

	for _, c := range f.conflicts {
		for _, l := range wrapNote(errorStyle, c, noteWidth-2) {
			notes = append(notes, "  "+l)
		}
	}

	if f.warning != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(hintStaleStyle, f.warning+" Press "+m.keys.keysFor(ActionSelect)+
			" again to add it to the Changeset anyway.", noteWidth)...)
	}

	notes = append(notes, "", hintStyle.Render(m.keys.keysFor(ActionNextPane)+" next field · "+
		m.keys.keysFor(ActionSelect)+" add to the Changeset · "+m.keys.keysFor(ActionClose)+" cancel"))

	return dialogView("Apply a Template to "+f.zone.Name, fields, f.field, true, notes)
}
