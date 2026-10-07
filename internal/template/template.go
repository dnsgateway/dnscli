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
	// wildcard sets own the Records at every name their wildcard name,
	// such as *._domainkey, covers, as well as at the wildcard itself.
	wildcard bool
	want     []want
	// merge, when set, makes the value the set wants from the value of
	// the single Record of the wanted type it owns, if it takes it.
	merge func(old string) (string, bool)
}

// owns reports whether a Record, named absolutely, belongs to the set.
func (s recordSet) owns(name string, r dns.Record) bool {
	if !slices.Contains(s.types, r.Type) {
		return false
	}

	if !sameName(r.Name, name) && (!s.wildcard || !covers(name, r.Name)) {
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
	return []Template{google(), proton(), parked()}
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

		if s.merge != nil {
			if old, ok := s.single(name, current); ok && old.Type == s.want[0].typ {
				if value, ok := s.merge(old.Value); ok {
					s.want = []want{{old.Type, value}}
				}
			}
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

// single returns the one Record of current the set owns at name; false
// when it owns none or several.
func (s recordSet) single(name string, current []dns.Record) (dns.Record, bool) {
	var (
		out dns.Record
		n   int
	)

	for _, r := range current {
		if s.owns(name, r) {
			out = r
			n++
		}
	}

	return out, n == 1
}

// Prefill returns the parameter values the Template starts with for a
// Zone holding current: the defaults, but for a mail Template the DMARC
// policy and report addresses of the Zone's DMARC Record, if it has one.
func (t Template) Prefill(zone string, current []dns.Record) Values {
	v := Values{}
	for _, p := range t.Params {
		v[p.Name] = p.Default
	}

	if _, ok := v["dmarc_policy"]; !ok {
		return v
	}

	tags, ok := existingDMARC(zone, current)
	if !ok {
		return v
	}

	if p, ok := tagValue(tags, "p"); ok && slices.Contains(dmarcPolicies, strings.ToLower(p)) {
		v["dmarc_policy"] = strings.ToLower(p)
	}

	rua, _ := tagValue(tags, "rua")
	v["dmarc_rua"] = normalRUA(rua)

	return v
}

// Warning says what the Template's values would weaken in a Zone holding
// current, such as its DMARC policy; empty when nothing.
func (t Template) Warning(zone string, current []dns.Record, given Values) string {
	v, err := t.resolve(given)
	if err != nil {
		return ""
	}

	policy, ok := v["dmarc_policy"]
	if !ok {
		return ""
	}

	tags, ok := existingDMARC(zone, current)
	if !ok {
		return ""
	}

	old, _ := tagValue(tags, "p")
	old = strings.ToLower(old)

	if slices.Index(dmarcPolicies, policy) >= slices.Index(dmarcPolicies, old) {
		return ""
	}

	return fmt.Sprintf("The DMARC policy of %s is %s: %s is weaker, and receivers will act less on mail that fails DMARC.",
		strings.TrimSuffix(zone, "."), old, policy)
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
			return w.typ == r.Type && sameName(r.Name, name) && normalValue(r.Type, r.Value) == normalValue(w.typ, w.value)
		})
		if i >= 0 {
			missing = slices.Delete(missing, i, i+1)

			continue
		}

		extra = append(extra, r)
	}

	if len(extra) == 1 && len(missing) == 1 && extra[0].Type == missing[0].typ && sameName(extra[0].Name, name) {
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

// covers reports whether a wildcard name, such as *._domainkey.example.com,
// covers a name below it, such as google._domainkey.example.com.
func covers(wildcard, name string) bool {
	base := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(wildcard, "*"), "."))
	name = strings.ToLower(strings.TrimSuffix(name, "."))

	return strings.HasPrefix(base, ".") && strings.HasSuffix(name, base) && len(name) > len(base)
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
			Hint: "An address for the aggregate reports, or several separated by commas; optional.",
		},
	}
}

// dmarcRecord is the DMARC Record of a mail Template.
func dmarcRecord(v Values) recordSet {
	value := "v=DMARC1; p=" + v["dmarc_policy"]
	if v["dmarc_rua"] != "" {
		value += "; rua=" + v["dmarc_rua"]
	}

	s := dmarc(value)
	s.merge = func(old string) (string, bool) {
		return mergeDMARC(old, v["dmarc_policy"], v["dmarc_rua"])
	}

	return s
}

// existingDMARC returns the tags of a Zone's DMARC Record: the single
// TXT or CNAME Record at _dmarc, if it is a DMARC TXT Record.
func existingDMARC(zone string, current []dns.Record) ([]string, bool) {
	name, err := dns.QualifyName(zone, "_dmarc")
	if err != nil {
		return nil, false
	}

	r, ok := dmarc("").single(name, current)
	if !ok || r.Type != "TXT" {
		return nil, false
	}

	return dmarcTags(r.Value)
}

// dmarcTags splits a DMARC value into its tags, trimmed; false when it is
// not a DMARC value.
func dmarcTags(value string) ([]string, bool) {
	var tags []string

	for _, tag := range strings.Split(unquote(strings.TrimSpace(value)), ";") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}

	return tags, len(tags) > 0 && tagKey(tags[0]) == "v" && strings.EqualFold(tagOf(tags[0]), "DMARC1")
}

// tagKey is the key of a tag, such as p in p=reject, in lower case.
func tagKey(tag string) string {
	k, _, _ := strings.Cut(tag, "=")

	return strings.ToLower(strings.TrimSpace(k))
}

// tagOf is the value of a tag, such as reject in p=reject.
func tagOf(tag string) string {
	_, v, _ := strings.Cut(tag, "=")

	return strings.TrimSpace(v)
}

// tagValue returns the value of the tag with the key.
func tagValue(tags []string, key string) (string, bool) {
	for _, tag := range tags {
		if tagKey(tag) == key {
			return tagOf(tag), true
		}
	}

	return "", false
}

// normalRUA is a DMARC report URI list in the form checkMailto returns it.
func normalRUA(rua string) string {
	if c, err := checkMailto(rua); err == nil {
		return c
	}

	return rua
}

// mergeDMARC sets the policy and report addresses of an existing DMARC
// value and keeps its other tags, such as sp, adkim or pct. A value that
// already has them is returned as it is; false when old is not a DMARC
// value.
func mergeDMARC(old, policy, rua string) (string, bool) {
	tags, ok := dmarcTags(old)
	if !ok {
		return "", false
	}

	p, _ := tagValue(tags, "p")
	r, _ := tagValue(tags, "rua")

	if strings.EqualFold(p, policy) && normalRUA(r) == rua {
		return old, true
	}

	var (
		out          []string
		hasP, hasRUA bool
	)

	for _, tag := range tags {
		switch tagKey(tag) {
		case "p":
			tag, hasP = "p="+policy, true
		case "rua":
			hasRUA = true

			if rua == "" {
				continue
			}

			tag = "rua=" + rua
		}

		out = append(out, tag)
	}

	if !hasP {
		out = slices.Insert(out, 1, "p="+policy)
	}

	if !hasRUA && rua != "" {
		i := slices.IndexFunc(out, func(tag string) bool { return tagKey(tag) == "p" })
		out = slices.Insert(out, i+1, "rua="+rua)
	}

	return strings.Join(out, "; "), true
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

func proton() Template {
	dkim := func(n string) Param {
		return Param{
			Name: "dkim" + n, Label: "DKIM target " + n, Required: true, check: checkHost,
			Hint: "The value of the protonmail" + strings.TrimPrefix(n, "1") + "._domainkey CNAME on the DKIM tab.",
		}
	}

	return Template{
		Name:    "Proton mail",
		Summary: "Mail through Proton: the verification token, its MX, an SPF that includes Proton, the three DKIM CNAMEs and a DMARC policy.",
		Params: append([]Param{
			{
				Name: "verification", Label: "Verification token", Required: true, check: checkProtonToken,
				Hint: "The protonmail-verification= value on the Verify tab.",
			},
			dkim("1"), dkim("2"), dkim("3"),
		}, dmarcParams()...),
		sets: func(v Values) []recordSet {
			return []recordSet{
				{name: "@", types: []string{"TXT"}, prefix: protonVerification, want: []want{{"TXT", v["verification"]}}},
				mxSet(want{"MX", "10 mail.protonmail.ch"}, want{"MX", "20 mailsec.protonmail.ch"}),
				spf("v=spf1 include:_spf.protonmail.ch ~all"),
				cnameAt("protonmail._domainkey", v["dkim1"]),
				cnameAt("protonmail2._domainkey", v["dkim2"]),
				cnameAt("protonmail3._domainkey", v["dkim3"]),
				dmarcRecord(v),
			}
		},
	}
}

const protonVerification = "protonmail-verification="

var tokenRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// checkProtonToken takes Proton's verification token, with or without
// its "protonmail-verification=" prefix, and returns the TXT value.
func checkProtonToken(v string) (string, error) {
	token := v
	if len(token) > len(protonVerification) && strings.EqualFold(token[:len(protonVerification)], protonVerification) {
		token = token[len(protonVerification):]
	}

	if !tokenRE.MatchString(token) {
		return "", fmt.Errorf("%q is not a verification token", v)
	}

	return protonVerification + token, nil
}

// checkHost takes a host name, such as a CNAME target, and returns it
// without a trailing dot.
func checkHost(v string) (string, error) {
	v = strings.TrimSuffix(v, ".")
	if err := dns.CheckValue("CNAME", v); err != nil {
		return "", fmt.Errorf("%q is not a host name", v)
	}

	return v, nil
}

// checkMailto takes email addresses, separated by commas, each with or
// without "mailto:" and a size limit such as !10m, and returns them as
// DMARC report URIs.
func checkMailto(v string) (string, error) {
	uris := strings.Split(v, ",")

	for i, u := range uris {
		addr := strings.TrimSpace(u)
		if len(addr) > len("mailto:") && strings.EqualFold(addr[:len("mailto:")], "mailto:") {
			addr = addr[len("mailto:"):]
		}

		limit := sizeLimitRE.FindString(addr)
		addr = strings.TrimSuffix(addr, limit)

		a, err := mail.ParseAddress(addr)
		if err != nil || a.Address != addr || strings.ContainsAny(addr, ";,! ") {
			return "", fmt.Errorf("%q is not an email address", v)
		}

		uris[i] = "mailto:" + addr + limit
	}

	return strings.Join(uris, ","), nil
}

// sizeLimitRE is the size limit a DMARC report URI may end with.
var sizeLimitRE = regexp.MustCompile(`![0-9]+[kmgtKMGT]?$`)

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
		tags := strings.Split(v, ";")
		for i, tag := range tags {
			// A key copied from a console may be broken over lines.
			lead := tag[:len(tag)-len(strings.TrimLeft(tag, " \t\r\n"))]
			if k, ok := strings.CutPrefix(strings.TrimSpace(tag), "p="); ok {
				k = strings.Join(strings.Fields(k), "")
				if base64RE.MatchString(k) {
					tags[i] = lead + "p=" + k

					return strings.Join(tags, ";"), nil
				}
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
		Summary: "No mail to or from the domain: a null MX, an SPF and a DMARC reject policy, and an empty DKIM key for every selector, in place of every DKIM Record under _domainkey.",
		sets: func(Values) []recordSet {
			return []recordSet{
				mxSet(want{"MX", "0 ."}),
				spf("v=spf1 -all"),
				dmarc("v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s"),
				{name: "*._domainkey", types: []string{"TXT", "CNAME"}, wildcard: true, want: []want{{"TXT", "v=DKIM1; p="}}},
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

// cnameAt is a single CNAME Record that replaces every TXT and CNAME
// Record at its name, such as a DKIM key a mail service hosts.
func cnameAt(name, target string) recordSet {
	return recordSet{name: name, types: []string{"TXT", "CNAME"}, want: []want{{"CNAME", target}}}
}
