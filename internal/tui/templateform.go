package tui

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider"

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
	// DMARC policy; a second Save stages it anyway.
	warning string
	// caret is the focused text field's caret, in runes back from its end.
	caret int
	// picker lists every value of the focused option field; nil when closed.
	picker *picker
	// size is the largest size the dialog takes, which it keeps.
	size dialogSize
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

	f.size = f.measure()
	m.templateForm = f

	return nil
}

func (f *templateForm) template() template.Template {
	return f.templates[f.pick]
}

// param returns the parameter of the focused field; false on the
// Template field and on Save.
func (f *templateForm) param() (template.Param, bool) {
	if f.field == 0 || f.field > len(f.template().Params) {
		return template.Param{}, false
	}

	return f.template().Params[f.field-1], true
}

// templateFormKey handles a key while the Template dialog is open. Like
// the Record form, Up and Down move between the fields, Left and Right
// change an option field's value, Enter opens an option field's picker or
// moves on from a text field, and Save stages the Template's changes.
func (m *Model) templateFormKey(msg tea.KeyMsg) tea.Cmd {
	f := m.templateForm
	a := m.keys.action(msg.String())
	rows := 1 + len(f.template().Params) + 1 // the Template, its parameters, then Save
	values := f.values[f.template().Name]

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if f.picker != nil {
		if f.picker.key(a) {
			f.picker = nil
		}

		return nil
	}

	if p, ok := f.param(); ok && p.Choices == nil {
		v := values[p.Name]
		if m.keys.typeInto(&v, &f.caret, msg) {
			values[p.Name] = v
			f.warning = ""

			return nil
		}
	}

	switch a {
	case ActionClose:
		m.templateForm = nil
	case ActionNextPane, ActionDown:
		f.field = (f.field + 1) % rows
	case ActionPrevPane, ActionUp:
		f.field = (f.field + rows - 1) % rows
	case ActionPaneLeft, ActionPaneRight:
		if options, at, ok := f.options(); ok {
			step := 1
			if a == ActionPaneLeft {
				step = len(options) - 1
			}

			f.setOption(f.field, (at+step)%len(options))
		}
	case ActionSelect:
		if f.field == rows-1 {
			return m.submitTemplateForm()
		}

		if options, at, ok := f.options(); ok {
			field := f.field
			f.picker = &picker{title: f.fieldLabel(), options: options, cursor: at, pick: func(i int) { f.setOption(field, i) }}

			return nil
		}

		f.field++
	}

	return nil
}

// options returns the values of the focused field and the index of its
// current one; ok is false for a field that is typed, not picked, and
// for Save.
func (f *templateForm) options() (options []string, at int, ok bool) {
	if f.field == 0 {
		names := make([]string, len(f.templates))
		for i, t := range f.templates {
			names[i] = t.Name
		}

		return names, f.pick, true
	}

	p, ok := f.param()
	if !ok || p.Choices == nil {
		return nil, 0, false
	}

	return p.Choices, max(slices.Index(p.Choices, f.values[f.template().Name][p.Name]), 0), true
}

// setOption sets option field field to its i-th value.
func (f *templateForm) setOption(field, i int) {
	if field == 0 {
		f.pick = i
		f.err, f.conflicts, f.warning = "", nil, ""

		return
	}

	p := f.template().Params[field-1]
	f.values[f.template().Name][p.Name] = p.Choices[i]
	f.warning = ""
}

// fieldLabel is the label of the focused field.
func (f *templateForm) fieldLabel() string {
	if p, ok := f.param(); ok {
		return p.Label
	}

	return "Template"
}

// submitTemplateForm stages the changes the picked Template makes to the
// open Zone, as its pending Changeset would leave it, into that
// Changeset. The Template merges with the pending changes but never
// overrides one: if it would change a Record the user has a pending change
// for, or bring back one staged for deletion, it stages nothing and lists
// those changes. If it would weaken something, such as the Zone's DMARC
// policy, it says so first and stages on a second Save.
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
		if pe, ok := errors.AsType[*template.ParamError](err); ok {
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

	// Each modification and deletion is of a Record of current, so of a
	// row; take it, and refuse to touch a row that has a pending change.
	of := make([]recordRow, len(changes))

	for i, c := range changes {
		// An addition or a modification must not bring back a Record the
		// user staged for deletion.
		if c.Kind != dns.ChangeDelete {
			for _, row := range m.rows {
				if row.kind == dns.ChangeDelete && template.SameData(row.rec, c.After) {
					f.conflicts = append(f.conflicts, describeRecord(z.Name, row.rec)+" (pending deletion)")
				}
			}
		}

		if c.Kind == dns.ChangeAdd {
			continue
		}

		j := slices.IndexFunc(rows, func(r recordRow) bool { return sameRecord(r.rec, c.Before) })
		row := rows[j]
		of[i] = row
		rows = slices.Delete(rows, j, j+1)

		switch {
		case row.change >= 0:
			f.conflicts = append(f.conflicts, describeRecord(z.Name, row.rec))
		case row.base.ProviderID == "":
			// Without its Provider ID, it cannot be changed at the Provider.
			// That alone stops the Template, whatever it conflicts with.
			f.conflicts = nil
			f.err = fmt.Sprintf("Nothing was staged: %s would change %s, which has no Provider ID, so dnscli cannot change it.",
				t.Name, describeRecord(z.Name, row.rec))

			return nil
		}
	}

	if len(f.conflicts) > 0 {
		f.err = fmt.Sprintf("Nothing was staged: %s would change Records you have pending changes for. "+
			"Discard them (%s, then %s) or make them what the Template wants, and apply it again:",
			t.Name, m.recordsKeys(ActionPreviewChanges), m.keys.keysFor(ActionDiscardChanges))

		return nil
	}

	if w := t.Warning(z.Name, current, f.values[t.Name]); w != "" && w != f.warning {
		f.warning = w

		return nil
	}

	// None of the rows has a pending change: stage the whole batch, then
	// rebuild the rows once.
	for i, c := range changes {
		switch c.Kind {
		case dns.ChangeAdd:
			m.addChange(z, c.After)
		case dns.ChangeModify:
			m.editChange(z, of[i], unproxied(c.After))
		case dns.ChangeDelete:
			m.deleteChange(z, of[i])
		}
	}

	m.rebuildRecordRows()

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
	r := m.refreshOf(zoneID).caps.ttlRange()
	if r.Auto {
		return dnsprovider.TTLAuto
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

	fields := f.dialogFields(t, values)
	if f.field < len(fields) {
		fields[f.field].caret = f.caret
	}

	notes := []string{saveRow(f.field == len(fields))}

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
		notes = append(notes, wrapNote(hintStaleStyle, f.warning+" Save again to add it to the Changeset anyway.", noteWidth)...)
	}

	return sizedDialogView(f.title(), fields, f.field, true, notes, f.hints(t, f.field), f.size)
}

func (f *templateForm) title() string {
	return "Apply a Template to " + f.zone.Name
}

// dialogFields are the rows of the dialog for Template t with values: the
// Template, then its parameters.
func (f *templateForm) dialogFields(t template.Template, values template.Values) []dialogField {
	fields := make([]dialogField, 0, 1+len(t.Params))
	fields = append(fields, dialogField{label: "Template", value: t.Name, choice: true})

	for _, p := range t.Params {
		fields = append(fields, dialogField{label: p.Label, value: values[p.Name], choice: p.Choices != nil})
	}

	return fields
}

// hints are the lines at the bottom of the dialog for Template t with
// field focused: the Template's summary and the focused parameter's hint.
func (f *templateForm) hints(t template.Template, field int) []string {
	hints := wrapNote(hintStyle, t.Summary, noteWidth)

	if field > 0 && field <= len(t.Params) && t.Params[field-1].Hint != "" {
		hints = append(hints, "")
		hints = append(hints, wrapNote(hintStyle, t.Params[field-1].Hint, noteWidth)...)
	}

	return hints
}

// measure works out the largest size the dialog takes, over every
// Template and focused field, with each typed value as wide as the dialog
// shows one and each choice at its widest. The dialog then keeps that size
// as the user moves through it; only an error or warning grows it.
func (f *templateForm) measure() dialogSize {
	var size dialogSize

	widest := strings.Repeat("W", maxValueWidth)

	for _, t := range f.templates {
		values := template.Values{}

		for _, p := range t.Params {
			values[p.Name] = widest
			if p.Choices != nil {
				values[p.Name] = slices.MaxFunc(p.Choices, func(a, b string) int { return len(a) - len(b) })
			}
		}

		fields := f.dialogFields(t, values)
		fields[0].value = slices.MaxFunc(f.templates, func(a, b template.Template) int { return len(a.Name) - len(b.Name) }).Name

		for field := range len(fields) + 1 {
			notes := []string{saveRow(field == len(fields))}
			size = size.grow(sizedDialogView(f.title(), fields, field, true, notes, f.hints(t, field), dialogSize{}))
		}
	}

	return size
}
