// Package template holds dnscli's built-in Templates: named sets of
// Records, with typed parameters, that turn into the changes that bring a
// Zone's Records in line with them.
//
// Templates are pure: Changes takes the zone name, the Zone's current
// Records and the parameter values, and returns the changes. It never
// touches a Record the Template does not own.
package template

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
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

// Param is one typed parameter of a Template.
type Param struct {
	// Name identifies the parameter in Values.
	Name string
	// Label is what the UI calls it.
	Label string
	// Hint says what the parameter takes, or where to find it.
	Hint string
	// Default is the value an empty one stands for.
	Default string
	// Required parameters must have a value.
	Required bool
	// Choices, when set, are the only values the parameter takes, and the
	// UI offers them to pick from.
	Choices []string

	// check checks a non-empty value and returns it in the form the
	// Template uses; nil takes any value as it is.
	check func(string) (string, error)
}

// Values are a Template's parameter values, by Param.Name.
type Values map[string]string

// ParamError is a parameter value a Template does not take.
type ParamError struct {
	// Param is the Name of the parameter.
	Param string
	Err   error
}

func (e *ParamError) Error() string {
	return e.Err.Error()
}

func (e *ParamError) Unwrap() error {
	return e.Err
}

// resolve checks the values given for t's parameters, and returns them
// in the form the Template uses, with the defaults filled in.
func (t Template) resolve(given Values) (Values, error) {
	out := Values{}

	for _, p := range t.Params {
		v := strings.TrimSpace(given[p.Name])
		if v == "" {
			v = p.Default
		}

		var err error

		switch {
		case v == "" && p.Required:
			err = fmt.Errorf("enter the %s", p.Label)
		case v == "":
		case p.Choices != nil && !slices.Contains(p.Choices, v):
			err = fmt.Errorf("the %s is one of %s, not %q", p.Label, strings.Join(p.Choices, ", "), v)
		case p.check != nil:
			v, err = p.check(v)
			if err != nil {
				err = fmt.Errorf("the %s: %w", p.Label, err)
			}
		}

		if err != nil {
			return nil, &ParamError{Param: p.Name, Err: err}
		}

		out[p.Name] = v
	}

	return out, nil
}

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
	return []Template{google(), parked()}
}

// Changes returns the changes that make current hold the Template's
// Records, each added with the given TTL.
func (t Template) Changes(zone string, current []dns.Record, v Values, ttl uint32) ([]dns.Change, error) {
	v, err := t.resolve(v)
	if err != nil {
		return nil, err
	}

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

// DMARC policies, from monitoring only to rejecting what fails.
var dmarcPolicies = []string{"none", "quarantine", "reject"}

// dmarcParams are the DMARC parameters of a mail Template.
func dmarcParams() []Param {
	return []Param{
		{
			Name: "dmarc_policy", Label: "DMARC policy", Default: "none", Choices: dmarcPolicies,
			Hint: "What receivers do with mail that fails DMARC: none only reports it.",
		},
		{
			Name: "dmarc_rua", Label: "DMARC reports to", check: checkMailto,
			Hint: "An address for the aggregate reports; optional.",
		},
	}
}

// dmarcRecord is the DMARC Record of a mail Template.
func dmarcRecord(v Values) recordSet {
	value := "v=DMARC1; p=" + v["dmarc_policy"]
	if v["dmarc_rua"] != "" {
		value += "; rua=" + v["dmarc_rua"]
	}

	return dmarc(value)
}

func google() Template {
	return Template{
		Name:    "Google mail",
		Summary: "Mail through Google Workspace: its MX, an SPF that includes Google, a DMARC policy and, when given, the DKIM key.",
		Params: append(dmarcParams(),
			Param{
				Name: "dkim_selector", Label: "DKIM selector", Default: "google", check: checkSelector,
				Hint: "The prefix the Admin console shows with the DKIM key.",
			},
			Param{
				Name: "dkim_key", Label: "DKIM key", check: checkDKIMKey,
				Hint: "The DKIM TXT value or public key from the Admin console; optional.",
			},
		),
		sets: func(v Values) []recordSet {
			sets := []recordSet{
				mxSet(want{"MX", "1 smtp.google.com"}),
				spf("v=spf1 include:_spf.google.com ~all"),
				dmarcRecord(v),
			}

			if v["dkim_key"] != "" {
				sets = append(sets, txtAt(v["dkim_selector"]+"._domainkey", v["dkim_key"]))
			}

			return sets
		},
	}
}

// checkMailto takes an email address, with or without "mailto:", and
// returns it as a DMARC report URI.
func checkMailto(v string) (string, error) {
	addr := v
	if len(addr) > len("mailto:") && strings.EqualFold(addr[:len("mailto:")], "mailto:") {
		addr = addr[len("mailto:"):]
	}

	a, err := mail.ParseAddress(addr)
	if err != nil || a.Address != addr || strings.ContainsAny(addr, ";,! ") {
		return "", fmt.Errorf("%q is not an email address", v)
	}

	return "mailto:" + addr, nil
}

var selectorRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,63}(\.[A-Za-z0-9_-]{1,63})*$`)

// checkSelector takes a DKIM selector: one or more DNS labels.
func checkSelector(v string) (string, error) {
	if !selectorRE.MatchString(v) {
		return "", fmt.Errorf("%q is not a DKIM selector, such as google", v)
	}

	return v, nil
}

var base64RE = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

// checkDKIMKey takes a DKIM public key, either bare, in base64, or as the
// whole DKIM TXT value, quoted or not, and returns the TXT value.
func checkDKIMKey(v string) (string, error) {
	v = unquote(v)

	if len(v) >= 7 && strings.EqualFold(v[:7], "v=DKIM1") {
		for _, tag := range strings.Split(v, ";") {
			if k, ok := strings.CutPrefix(strings.TrimSpace(tag), "p="); ok && base64RE.MatchString(k) {
				return v, nil
			}
		}

		return "", errors.New("the DKIM value has no public key (p=)")
	}

	key := strings.Join(strings.Fields(v), "")
	if !base64RE.MatchString(key) {
		return "", errors.New("a DKIM key is in base64, such as MIIBIjANBgkqh…, or the whole v=DKIM1 value")
	}

	return "v=DKIM1; k=rsa; p=" + key, nil
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
