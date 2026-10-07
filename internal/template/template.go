// Package template holds dnscli's built-in Templates: named sets of
// Records, with typed parameters, that turn into the changes that bring a
// Zone's Records in line with them.
//
// Templates are pure: Changes takes the zone name, the Zone's current
// Records and the parameter values, and returns the changes. It never
// touches a Record the Template does not own.
package template

import (
	"slices"
	"strings"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// Template is a built-in, named set of Records.
type Template struct {
	Name string
	// Summary says in a sentence what the Template publishes.
	Summary string
	// Params are the parameters the Template asks for, in order.
	Params []Param

	// sets lists the Record sets the Template owns, from the parameter
	// values, already checked and with their defaults filled in.
	sets func(v Values) []recordSet
}

// Param is one parameter of a Template.
type Param struct {
	Name  string
	Label string
}

// Values are a Template's parameter values, by Param.Name.
type Values map[string]string

// recordSet is a set of Records the Template owns at one name: the
// current Records it matches are replaced by want.
type recordSet struct {
	// name is the owner name, relative to the Zone; "@" is the apex.
	name string
	// types are the Record types the set owns at name.
	types []string
	// prefix, when set, limits the set to the Records whose value starts
	// with it, in any case: an SPF Record is the apex TXT starting
	// "v=spf1", among other apex TXT Records.
	prefix string
	want   []want
}

// owns reports whether a Record, named absolutely, belongs to the set.
func (s recordSet) owns(name string, r dns.Record) bool {
	if !sameName(r.Name, name) || !slices.Contains(s.types, r.Type) {
		return false
	}

	if s.prefix == "" {
		return true
	}

	v := strings.ToLower(normalValue(r.Type, r.Value))
	p := strings.ToLower(s.prefix)

	return v == p || strings.HasPrefix(v, p+" ") || (strings.HasSuffix(p, "=") && strings.HasPrefix(v, p))
}

// want is a Record a Template wants, by type and value.
type want struct {
	typ, value string
}

// Builtin returns the built-in Templates, in the order the UI offers them.
func Builtin() []Template {
	return []Template{parked()}
}

// Changes returns the changes that make current hold the Template's
// Records, each added with the given TTL.
func (t Template) Changes(zone string, current []dns.Record, v Values, ttl uint32) ([]dns.Change, error) {
	var out []dns.Change

	for _, s := range t.sets(v) {
		name, err := dns.QualifyName(zone, s.name)
		if err != nil {
			return nil, err
		}

		for _, w := range s.want {
			if err := dns.CheckValue(w.typ, w.value); err != nil {
				return nil, err
			}
		}

		out = append(out, s.changes(name, current, ttl)...)
	}

	return out, nil
}

// changes replaces the Records of current that the set owns at name with
// the Records it wants. An owned Record that already holds a wanted value
// is kept. Of the rest, a single owned Record is modified into a single
// wanted one of its type; otherwise they are deleted and the wanted
// Records added.
func (s recordSet) changes(name string, current []dns.Record, ttl uint32) []dns.Change {
	missing := slices.Clone(s.want)

	var extra []dns.Record

	for _, r := range current {
		if !s.owns(name, r) {
			continue
		}

		i := slices.IndexFunc(missing, func(w want) bool {
			return w.typ == r.Type && normalValue(r.Type, r.Value) == normalValue(w.typ, w.value)
		})
		if i >= 0 {
			missing = slices.Delete(missing, i, i+1)

			continue
		}

		extra = append(extra, r)
	}

	if len(extra) == 1 && len(missing) == 1 && extra[0].Type == missing[0].typ {
		after := extra[0]
		after.Type, after.Name, after.Value, after.TTL = missing[0].typ, name, missing[0].value, ttl

		return []dns.Change{{Kind: dns.ChangeModify, Before: extra[0], After: after}}
	}

	var out []dns.Change

	for _, r := range extra {
		out = append(out, dns.Change{Kind: dns.ChangeDelete, Before: r})
	}

	for _, w := range missing {
		out = append(out, dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: w.typ, Name: name, Value: w.value, TTL: ttl}})
	}

	return out
}

// SameData reports whether two Records hold the same data: the same type,
// name and value, whatever their TTL, as DNS compares names and as the
// Template compares values. A Record a Template wants is already in place
// when the Zone has one with the same data.
func SameData(a, b dns.Record) bool {
	return a.Type == b.Type && sameName(a.Name, b.Name) && normalValue(a.Type, a.Value) == normalValue(b.Type, b.Value)
}

// sameName compares DNS names: in any case, with or without a trailing dot.
func sameName(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}

// normalValue is a Record value in the form the Templates compare: host
// names in lower case without a trailing dot, and TXT text without the
// quotes some Providers keep around it.
func normalValue(typ, value string) string {
	value = strings.TrimSpace(value)

	switch typ {
	case "TXT":
		return unquote(value)
	case "MX", "CNAME", "NS":
		f := strings.Fields(value)
		for i, x := range f {
			if x != "." {
				f[i] = strings.ToLower(strings.TrimSuffix(x, "."))
			}
		}

		return strings.Join(f, " ")
	}

	return value
}

// unquote joins the quoted character-strings of TXT text in zone-file
// form, such as "v=spf1 " "-all", into the text they hold. Text that is
// not quoted is returned as it is.
func unquote(s string) string {
	if !strings.HasPrefix(s, `"`) {
		return s
	}

	var b strings.Builder

	in, esc := false, false

	for _, r := range s {
		switch {
		case esc:
			b.WriteRune(r)
			esc = false
		case in && r == '\\':
			esc = true
		case r == '"':
			in = !in
		case in:
			b.WriteRune(r)
		case r != ' ' && r != '\t':
			return s // not a run of quoted strings
		}
	}

	if in || esc {
		return s
	}

	return b.String()
}

func parked() Template {
	return Template{
		Name:    "Parked domain",
		Summary: "No mail to or from the domain: a null MX, an SPF and a DMARC reject policy, and an empty DKIM key for every selector.",
		sets: func(Values) []recordSet {
			return []recordSet{
				mxSet(want{"MX", "0 ."}),
				spf("v=spf1 -all"),
				dmarc("v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s"),
				txtAt("*._domainkey", "v=DKIM1; p="),
			}
		},
	}
}

// mxSet is the apex MX set: a mail Template replaces every apex MX Record.
func mxSet(ws ...want) recordSet {
	return recordSet{name: "@", types: []string{"MX"}, want: ws}
}

// spf is the singleton SPF Record: the apex TXT starting "v=spf1".
func spf(value string) recordSet {
	return recordSet{name: "@", types: []string{"TXT"}, prefix: "v=spf1", want: []want{{"TXT", value}}}
}

// dmarc is the singleton DMARC Record: the TXT at _dmarc.
func dmarc(value string) recordSet {
	return txtAt("_dmarc", value)
}

// txtAt is a single TXT Record that replaces every TXT and CNAME Record at
// its name, such as a DKIM key at <selector>._domainkey.
func txtAt(name, value string) recordSet {
	return recordSet{name: name, types: []string{"TXT", "CNAME"}, want: []want{{"TXT", value}}}
}
