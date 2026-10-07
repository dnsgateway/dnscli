package tui

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// Fields of the Record form. The proxy flag is last, and shown only for
// the types the Zone's Provider can proxy.
const (
	recordFieldType = iota
	recordFieldName
	recordFieldValue
	recordFieldTTL
	recordFieldProxied
)

// proxiedExtra is the Record extra holding Cloudflare's proxy flag, which
// only the types the Provider can proxy take: changing a Record to another
// type drops it.
const proxiedExtra = provider.ProxiedExtra

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
	// proxied is the proxy flag, for the types the Provider can proxy.
	proxied bool
	err     string
	// edit is the row being edited; nil when adding.
	edit *recordRow
	// picker lists every value of the focused option field; nil when closed.
	picker *picker
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
		f.proxied = r.Extras[proxiedExtra] == "true"
	}

	return f
}

// recordFormKey handles a key while the Record form is open. Up and Down
// move between the fields, like Tab, and Left and Right change an option
// field's value. Enter opens an option field's picker, moves on from a
// text field, and on Save stages the Record.
func (m *Model) recordFormKey(msg tea.KeyMsg) tea.Cmd {
	f := m.recordForm
	a := m.keys.action(msg.String())
	rows := m.recordFormFields() + 1 // the fields, then Save
	f.field = min(f.field, rows-1)

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if f.picker != nil {
		if f.picker.key(a) {
			f.picker = nil
		}

		return nil
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
	case ActionNextPane, ActionDown:
		f.field = (f.field + 1) % rows
	case ActionPrevPane, ActionUp:
		f.field = (f.field + rows - 1) % rows
	case ActionPaneLeft, ActionPaneRight:
		if options, at, ok := f.options(); ok && f.field < rows-1 {
			step := 1
			if a == ActionPaneLeft {
				step = len(options) - 1
			}

			f.setOption((at + step) % len(options))
		}
	case ActionSelect:
		if f.field == rows-1 {
			return m.submitRecordForm()
		}

		if options, at, ok := f.options(); ok {
			f.picker = &picker{title: recordFieldLabels[f.field], options: options, cursor: at, pick: f.setOption}

			return nil
		}

		f.field++
	}

	return nil
}

// recordFieldLabels are the labels of the Record form's fields.
var recordFieldLabels = []string{"Type", "Name", "Value", "TTL", "Proxied"}

// onOff are the values of the proxy flag, as the form shows them.
var onOff = []string{"off", "on"}

// options returns the values of the focused field and the index of its
// current one; ok is false for a field that is typed, not picked. The
// caller rules out Save, which takes the proxy flag's place when the form
// has none.
func (f *recordForm) options() (options []string, at int, ok bool) {
	switch f.field {
	case recordFieldType:
		return dns.EditableTypes, f.typ, true
	case recordFieldProxied:
		if f.proxied {
			return onOff, 1, true
		}

		return onOff, 0, true
	}

	return nil, 0, false
}

// setOption sets the focused option field to its i-th value.
func (f *recordForm) setOption(i int) {
	switch f.field {
	case recordFieldType:
		f.typ = i
	case recordFieldProxied:
		f.proxied = i == 1
	}
}

// submitRecordForm validates the form and stages the Record in the
// pending Changeset. The write gate is checked again: the Zone may have
// gone stale while the form was open.
func (m *Model) submitRecordForm() tea.Cmd {
	f := m.recordForm
	f.err = ""

	r, field, err := f.record(m.refreshOf(f.zone.ID).caps)
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

// record builds the Record the form describes, with a TTL in the Zone's
// Provider's range and its proxy flag, or says what is wrong with it and
// in which field.
func (f *recordForm) record(caps providerCaps) (dns.Record, int, string) {
	ttlRange := caps.ttlRange()

	typ := dns.EditableTypes[f.typ]

	name, err := dns.QualifyName(f.zone.Name, f.name)
	if err != nil {
		return dns.Record{}, recordFieldName, capitalise(err.Error()) + "."
	}

	// An edit that leaves the name as it was keeps the Record's own case,
	// whatever the case of the zone name.
	if f.edit != nil && strings.TrimSpace(f.name) == dns.RelativeName(f.zone.Name, f.edit.rec.Name) {
		name = strings.TrimSuffix(f.edit.rec.Name, ".")
	}

	value := strings.TrimSpace(f.value)
	if err := dns.CheckValue(typ, value); err != nil {
		return dns.Record{}, recordFieldValue, capitalise(err.Error()) + "."
	}

	ttl := uint64(dns.TTLAuto)
	if s := strings.TrimSpace(f.ttl); s != "auto" {
		ttl, err = strconv.ParseUint(s, 10, 32)
		if err != nil {
			return dns.Record{}, recordFieldTTL, capitalise(ttlRange.Check(0).Error()) + "."
		}
	}

	if err := ttlRange.Check(uint32(ttl)); err != nil {
		return dns.Record{}, recordFieldTTL, capitalise(err.Error()) + "."
	}

	r := dns.Record{Type: typ, Name: name, Value: value, TTL: uint32(ttl)}

	// An edit keeps the Record's identity and its Provider-specific fields,
	// such as Cloudflare's proxy flag, which the form does not show.
	if f.edit != nil {
		r.ProviderID = f.edit.rec.ProviderID
		r.Extras = maps.Clone(f.edit.rec.Extras)
	}

	_, had := r.Extras[proxiedExtra]

	switch {
	case !f.proxiable(caps.proxiable):
		delete(r.Extras, proxiedExtra)
	case had || f.proxied:
		// A Record that had no flag gets one only when it is turned on.
		if r.Extras == nil {
			r.Extras = map[string]string{}
		}

		r.Extras[proxiedExtra] = strconv.FormatBool(f.proxied)
	}

	return r, 0, ""
}

// proxiable reports whether the form offers the proxy flag: for a type the
// Zone's Provider can proxy, and, when editing a cached Record, only if
// the Provider marked it proxiable, by giving it the flag. Cloudflare
// cannot proxy every Record of a proxiable type, such as one with a
// private address.
func (f *recordForm) proxiable(types []string) bool {
	if !slices.Contains(types, dns.EditableTypes[f.typ]) {
		return false
	}

	if f.edit == nil || f.edit.kind == dns.ChangeAdd {
		return true
	}

	_, had := f.edit.base.Extras[proxiedExtra]

	return had
}

// recordFormFields is how many fields the Record form shows: the proxy
// flag only where the form offers it.
func (m *Model) recordFormFields() int {
	f := m.recordForm
	if f.proxiable(m.refreshOf(f.zone.ID).caps.proxiable) {
		return recordFieldProxied + 1
	}

	return recordFieldProxied
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

	if m.recordFormFields() > recordFieldProxied {
		v := "off"
		if f.proxied {
			v = "on"
		}

		fields = append(fields, dialogField{label: "Proxied", value: v, choice: true})
	}

	notes := []string{saveRow(f.field == len(fields))}

	if f.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, f.err, noteWidth)...)
	}

	hint := "Zone " + f.zone.Name + ": a name is relative to it, or absolute with a trailing dot; @ is the apex."
	if m.refreshOf(f.zone.ID).caps.autoTTL {
		hint += " TTL 1 is automatic."
	}

	return sizedDialogView(title, fields, f.field, true, notes, wrapNote(hintStyle, hint, noteWidth), dialogSize{})
}
