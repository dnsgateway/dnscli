package tui

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// Fields of the Record form.
const (
	recordFieldType = iota
	recordFieldName
	recordFieldValue
	recordFieldTTL
	recordFieldCount
)

// defaultTTL is the TTL the Record form offers for a new Record.
const defaultTTL = 300

// recordForm adds a Record to the open Zone's pending Changeset, or edits
// one of its rows.
type recordForm struct {
	zone  dns.Zone
	field int
	typ   int // index into dns.EditableTypes
	name  string
	value string
	ttl   string
	err   string
	// edit is the row being edited; nil when adding.
	edit *recordRow
}

// newRecordForm opens the Record form, prefilled with the row's Record
// when editing one.
func newRecordForm(z dns.Zone, edit *recordRow) *recordForm {
	f := &recordForm{zone: z, ttl: strconv.Itoa(defaultTTL), edit: edit}

	if edit != nil {
		r := edit.rec
		f.typ = max(slices.Index(dns.EditableTypes, r.Type), 0)
		f.name = dns.RelativeName(z.Name, r.Name)
		f.value = r.Value
		f.ttl = strconv.FormatUint(uint64(r.TTL), 10)
	}

	return f
}

// recordFormKey handles a key while the Record form is open.
func (m *Model) recordFormKey(msg tea.KeyMsg) tea.Cmd {
	f := m.recordForm
	a := m.keys.action(msg.String())

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	switch f.field {
	case recordFieldName:
		if m.keys.typeInto(&f.name, msg) {
			return nil
		}
	case recordFieldValue:
		if m.keys.typeInto(&f.value, msg) {
			return nil
		}
	case recordFieldTTL:
		if m.keys.typeInto(&f.ttl, msg) {
			return nil
		}
	}

	switch a {
	case ActionClose:
		m.recordForm = nil
	case ActionNextPane:
		f.field = (f.field + 1) % recordFieldCount
	case ActionPrevPane:
		f.field = (f.field + recordFieldCount - 1) % recordFieldCount
	case ActionUp, ActionDown:
		if f.field == recordFieldType {
			n := len(dns.EditableTypes)
			step := 1
			if a == ActionUp {
				step = n - 1
			}

			f.typ = (f.typ + step) % n
		}
	case ActionSelect:
		if f.field < recordFieldCount-1 {
			f.field++

			return nil
		}

		return m.submitRecordForm()
	}

	return nil
}

// submitRecordForm validates the form and stages the Record in the
// pending Changeset. The write gate is checked again: the Zone may have
// gone stale while the form was open.
func (m *Model) submitRecordForm() tea.Cmd {
	f := m.recordForm
	f.err = ""

	r, field, err := f.record()
	if err != "" {
		f.err, f.field = err, field

		return nil
	}

	z, cmd, ok := m.writeGate()
	if !ok || z.ID != f.zone.ID {
		f.err = m.notice.text

		return cmd
	}

	m.recordForm = nil

	if f.edit == nil {
		m.stageAdd(z, r)

		return nil
	}

	m.stageEdit(z, *f.edit, r)

	return nil
}

// record builds the Record the form describes, or says what is wrong with
// it and in which field.
func (f *recordForm) record() (dns.Record, int, string) {
	typ := dns.EditableTypes[f.typ]

	name, err := dns.QualifyName(f.zone.Name, f.name)
	if err != nil {
		return dns.Record{}, recordFieldName, capitalise(err.Error()) + "."
	}

	value := strings.TrimSpace(f.value)
	if err := dns.CheckValue(typ, value); err != nil {
		return dns.Record{}, recordFieldValue, capitalise(err.Error()) + "."
	}

	ttl := uint64(dns.TTLAuto)
	if s := strings.TrimSpace(f.ttl); s != "auto" {
		ttl, err = strconv.ParseUint(s, 10, 32)
		if err == nil {
			err = dns.CheckTTL(uint32(ttl))
		}

		if err != nil {
			return dns.Record{}, recordFieldTTL, capitalise(dns.CheckTTL(0).Error()) + "."
		}
	}

	r := dns.Record{Type: typ, Name: name, Value: value, TTL: uint32(ttl)}

	// An edit keeps the Record's identity and its Provider-specific fields,
	// such as Cloudflare's proxy flag, which the form does not show.
	if f.edit != nil {
		r.ProviderID = f.edit.rec.ProviderID
		r.Extras = maps.Clone(f.edit.rec.Extras)
	}

	return r, 0, ""
}

func capitalise(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

func (m *Model) recordFormView() string {
	f := m.recordForm

	title := "Add Record"
	if f.edit != nil {
		title = "Edit Record"
	}

	fields := []dialogField{
		{label: "Type", value: dns.EditableTypes[f.typ], choice: true},
		{label: "Name", value: f.name},
		{label: "Value", value: f.value},
		{label: "TTL", value: f.ttl},
	}

	notes := wrapNote(hintStyle, "Zone "+f.zone.Name+": a name is relative to it, or absolute with a trailing dot; @ is the apex. "+
		"TTL 1 is automatic.", noteWidth)

	if f.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, f.err, noteWidth)...)
	}

	notes = append(notes, "", hintStyle.Render(m.keys.keysFor(ActionNextPane)+" next field · "+
		m.keys.keysFor(ActionSelect)+" add to the Changeset · "+m.keys.keysFor(ActionClose)+" cancel"))

	return dialogView(title, fields, f.field, true, notes)
}
