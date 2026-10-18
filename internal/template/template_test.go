// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package template_test

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/template"
)

const zone = "example.com"

func byName(t *testing.T, name string) template.Template {
	t.Helper()

	for _, tpl := range template.Builtin() {
		if tpl.Name == name {
			return tpl
		}
	}

	t.Fatalf("no built-in Template %q", name)

	return template.Template{}
}

func add(typ, name, value string) dns.Change {
	return dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: typ, Name: name, Value: value, TTL: 3600}}
}

func TestParkedDomainOnAnEmptyZoneAddsRecordsThatBlockSpoofing(t *testing.T) {
	got, err := byName(t, "Parked domain").Changes(zone, nil, nil, 3600)
	if err != nil {
		t.Fatal(err)
	}

	want := []dns.Change{
		add("MX", "example.com", "0 ."),
		add("TXT", "example.com", "v=spf1 -all"),
		add("TXT", "_dmarc.example.com", "v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s"),
		add("TXT", "*._domainkey.example.com", "v=DKIM1; p="),
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes =\n%+v\nwant\n%+v", got, want)
	}
}

func rec(id, typ, name, value string, ttl uint32) dns.Record {
	return dns.Record{ProviderID: id, Type: typ, Name: name, Value: value, TTL: ttl}
}

func modify(before dns.Record, value string) dns.Change {
	after := before
	after.Value, after.TTL = value, 3600

	return dns.Change{Kind: dns.ChangeModify, Before: before, After: after}
}

func del(before dns.Record) dns.Change {
	return dns.Change{Kind: dns.ChangeDelete, Before: before}
}

var (
	webA      = rec("r1", "A", "example.com", "192.0.2.1", 300)
	otherTXT  = rec("r2", "TXT", "example.com", "google-site-verification=abc", 300)
	oldSPF    = rec("r3", "TXT", "example.com", "v=spf1 include:mail.example.net ~all", 300)
	oldSPF2   = rec("r4", "TXT", "example.com", "v=spf1 a mx -all", 300)
	oldDMARC  = rec("r5", "TXT", "_dmarc.example.com", "v=DMARC1; p=none; rua=mailto:a@example.com", 300)
	oldMX10   = rec("r6", "MX", "example.com", "10 mx1.example.net", 300)
	oldMX20   = rec("r7", "MX", "example.com", "20 mx2.example.net", 300)
	subSPF    = rec("r8", "TXT", "sub.example.com", "v=spf1 -all", 300)
	parkedMX  = rec("p1", "MX", "Example.COM.", "0 .", 60)
	parkedSPF = rec("p2", "TXT", "example.com", `"v=spf1 -all"`, 300)
	parkedDM  = rec("p3", "TXT", "_dmarc.example.com", "v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s", 300)
	parkedDK  = rec("p4", "TXT", "*._domainkey.example.com", "v=DKIM1; p=", 300)

	googleDKIM = rec("d1", "TXT", "google._domainkey.example.com", "v=DKIM1; k=rsa; p=MIIB", 300)
	protonDKIM = rec("d2", "CNAME", "protonmail._domainkey.Example.com.", "protonmail.domainkey.d.domains.proton.ch", 300)
)

func TestParkedDomainReplacesTheRecordsItOwnsAndLeavesTheRestAlone(t *testing.T) {
	cases := []struct {
		name    string
		current []dns.Record
		want    []dns.Change
	}{
		{
			name:    "one SPF and one DMARC are modified in place",
			current: []dns.Record{webA, otherTXT, oldSPF, oldDMARC, subSPF},
			want: []dns.Change{
				add("MX", "example.com", "0 ."),
				modify(oldSPF, "v=spf1 -all"),
				modify(oldDMARC, "v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s"),
				add("TXT", "*._domainkey.example.com", "v=DKIM1; p="),
			},
		},
		{
			name:    "two SPF Records are deleted and one added",
			current: []dns.Record{oldSPF, oldSPF2},
			want: []dns.Change{
				add("MX", "example.com", "0 ."),
				del(oldSPF), del(oldSPF2), add("TXT", "example.com", "v=spf1 -all"),
				add("TXT", "_dmarc.example.com", "v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s"),
				add("TXT", "*._domainkey.example.com", "v=DKIM1; p="),
			},
		},
		{
			name:    "the MX set is replaced",
			current: []dns.Record{oldMX10, oldMX20},
			want: []dns.Change{
				del(oldMX10), del(oldMX20), add("MX", "example.com", "0 ."),
				add("TXT", "example.com", "v=spf1 -all"),
				add("TXT", "_dmarc.example.com", "v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s"),
				add("TXT", "*._domainkey.example.com", "v=DKIM1; p="),
			},
		},
		{
			name: "every DKIM Record under _domainkey is deleted for the empty wildcard key",
			current: []dns.Record{
				parkedMX, parkedSPF, parkedDM, googleDKIM, protonDKIM,
				rec("d3", "TXT", "_domainkey.example.com", "o=-", 300),
				rec("d4", "TXT", "s1._domainkey.sub.example.com", "v=DKIM1; p=SUB", 300),
			},
			want: []dns.Change{
				del(googleDKIM), del(protonDKIM),
				add("TXT", "*._domainkey.example.com", "v=DKIM1; p="),
			},
		},
		{
			name:    "a DKIM Record holding the empty key at a selector is deleted too",
			current: []dns.Record{parkedMX, parkedSPF, parkedDM, parkedDK, rec("d5", "TXT", "s1._domainkey.example.com", "v=DKIM1; p=", 300)},
			want:    []dns.Change{del(rec("d5", "TXT", "s1._domainkey.example.com", "v=DKIM1; p=", 300))},
		},
		{
			name:    "Records already as the Template wants them are kept, whatever their TTL, case or quoting",
			current: []dns.Record{webA, parkedMX, parkedSPF, parkedDM, parkedDK},
			want:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := byName(t, "Parked domain").Changes(zone, tc.current, nil, 3600)
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("changes =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

// applied is current with changes applied, as a Provider would apply them.
func applied(current []dns.Record, changes []dns.Change) []dns.Record {
	out := slices.Clone(current)

	for i, c := range changes {
		switch c.Kind {
		case dns.ChangeAdd:
			r := c.After
			r.ProviderID = fmt.Sprintf("new-%d", i)
			out = append(out, r)
		case dns.ChangeModify, dns.ChangeDelete:
			j := slices.IndexFunc(out, func(r dns.Record) bool { return r.ProviderID == c.Before.ProviderID })
			if c.Kind == dns.ChangeDelete {
				out = slices.Delete(out, j, j+1)
			} else {
				out[j] = c.After
			}
		}
	}

	return out
}

var googleMX = rec("g1", "MX", "example.com", "1 smtp.google.com", 3600)

func TestGoogleMail(t *testing.T) {
	const key = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAu1+/x="

	cases := []struct {
		name    string
		current []dns.Record
		values  template.Values
		want    []dns.Change
	}{
		{
			name:    "the defaults on an empty Zone: no DKIM key, DMARC monitoring only",
			current: nil,
			want: []dns.Change{
				add("MX", "example.com", "1 smtp.google.com"),
				add("TXT", "example.com", "v=spf1 include:_spf.google.com ~all"),
				add("TXT", "_dmarc.example.com", "v=DMARC1; p=none"),
			},
		},
		{
			name:    "a DMARC policy and report address, and a DKIM key",
			current: nil,
			values:  template.Values{"dmarc_policy": "quarantine", "dmarc_rua": "dmarc@example.com", "dkim_key": key},
			want: []dns.Change{
				add("MX", "example.com", "1 smtp.google.com"),
				add("TXT", "example.com", "v=spf1 include:_spf.google.com ~all"),
				add("TXT", "_dmarc.example.com", "v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com"),
				add("TXT", "google._domainkey.example.com", "v=DKIM1; k=rsa; p="+key),
			},
		},
		{
			name:    "a DKIM key pasted as the whole TXT value, at another selector",
			current: nil,
			values:  template.Values{"dkim_selector": "g2026", "dkim_key": `"v=DKIM1; k=rsa; " "p=` + key + `"`, "dmarc_rua": "mailto:r@example.net"},
			want: []dns.Change{
				add("MX", "example.com", "1 smtp.google.com"),
				add("TXT", "example.com", "v=spf1 include:_spf.google.com ~all"),
				add("TXT", "_dmarc.example.com", "v=DMARC1; p=none; rua=mailto:r@example.net"),
				add("TXT", "g2026._domainkey.example.com", "v=DKIM1; k=rsa; p="+key),
			},
		},
		{
			name:    "a whole DKIM TXT value whose key is broken over lines",
			current: nil,
			values:  template.Values{"dkim_key": "v=DKIM1; k=rsa; p=" + key[:10] + " \n\t" + key[10:30] + "\n" + key[30:]},
			want: []dns.Change{
				add("MX", "example.com", "1 smtp.google.com"),
				add("TXT", "example.com", "v=spf1 include:_spf.google.com ~all"),
				add("TXT", "_dmarc.example.com", "v=DMARC1; p=none"),
				add("TXT", "google._domainkey.example.com", "v=DKIM1; k=rsa; p="+key),
			},
		},
		{
			name:    "a Zone with SPF, DMARC and another MX: they are replaced, the rest untouched",
			current: []dns.Record{webA, otherTXT, oldSPF, oldDMARC, oldMX10, subSPF},
			want: []dns.Change{
				modify(oldMX10, "1 smtp.google.com"),
				modify(oldSPF, "v=spf1 include:_spf.google.com ~all"),
				modify(oldDMARC, "v=DMARC1; p=none"),
			},
		},
		{
			name:    "the Google MX is kept and the other MX Records deleted",
			current: []dns.Record{oldMX10, googleMX, oldMX20},
			want: []dns.Change{
				del(oldMX10), del(oldMX20),
				add("TXT", "example.com", "v=spf1 include:_spf.google.com ~all"),
				add("TXT", "_dmarc.example.com", "v=DMARC1; p=none"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl := byName(t, "Google mail")

			got, err := tpl.Changes(zone, tc.current, tc.values, 3600)
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("changes =\n%+v\nwant\n%+v", got, tc.want)
			}

			// Applying the Template again changes nothing.
			again, err := tpl.Changes(zone, applied(tc.current, got), tc.values, 3600)
			if err != nil || len(again) != 0 {
				t.Fatalf("applying again = %+v, %v; want no changes", again, err)
			}
		})
	}
}

func TestProtonMail(t *testing.T) {
	values := template.Values{
		"verification": "protonmail-verification=0123456789abcdef",
		"dkim1":        "protonmail.domainkey.dabc.domains.proton.ch.",
		"dkim2":        "protonmail2.domainkey.dabc.domains.proton.ch",
		"dkim3":        "protonmail3.domainkey.dabc.domains.proton.ch",
		"dmarc_policy": "quarantine",
	}

	oldDKIM := rec("r9", "TXT", "protonmail._domainkey.example.com", "v=DKIM1; p=OLD", 300)
	oldVerify := rec("r10", "TXT", "example.com", "protonmail-verification=old", 300)

	cases := []struct {
		name    string
		current []dns.Record
		values  template.Values
		want    []dns.Change
	}{
		{
			name:   "an empty Zone",
			values: values,
			want: []dns.Change{
				add("TXT", "example.com", "protonmail-verification=0123456789abcdef"),
				add("MX", "example.com", "10 mail.protonmail.ch"),
				add("MX", "example.com", "20 mailsec.protonmail.ch"),
				add("TXT", "example.com", "v=spf1 include:_spf.protonmail.ch ~all"),
				add("CNAME", "protonmail._domainkey.example.com", "protonmail.domainkey.dabc.domains.proton.ch"),
				add("CNAME", "protonmail2._domainkey.example.com", "protonmail2.domainkey.dabc.domains.proton.ch"),
				add("CNAME", "protonmail3._domainkey.example.com", "protonmail3.domainkey.dabc.domains.proton.ch"),
				add("TXT", "_dmarc.example.com", "v=DMARC1; p=quarantine"),
			},
		},
		{
			name:    "a Zone with SPF, DMARC, MX, an old verification token and a TXT DKIM key",
			current: []dns.Record{webA, otherTXT, oldSPF, oldDMARC, oldMX10, oldVerify, oldDKIM},
			values:  template.Values{"verification": "0123456789abcdef", "dkim1": "a.example.net", "dkim2": "b.example.net", "dkim3": "c.example.net"},
			want: []dns.Change{
				modify(oldVerify, "protonmail-verification=0123456789abcdef"),
				del(oldMX10),
				add("MX", "example.com", "10 mail.protonmail.ch"),
				add("MX", "example.com", "20 mailsec.protonmail.ch"),
				modify(oldSPF, "v=spf1 include:_spf.protonmail.ch ~all"),
				del(oldDKIM),
				add("CNAME", "protonmail._domainkey.example.com", "a.example.net"),
				add("CNAME", "protonmail2._domainkey.example.com", "b.example.net"),
				add("CNAME", "protonmail3._domainkey.example.com", "c.example.net"),
				modify(oldDMARC, "v=DMARC1; p=none"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl := byName(t, "Proton mail")

			got, err := tpl.Changes(zone, tc.current, tc.values, 3600)
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("changes =\n%+v\nwant\n%+v", got, tc.want)
			}

			again, err := tpl.Changes(zone, applied(tc.current, got), tc.values, 3600)
			if err != nil || len(again) != 0 {
				t.Fatalf("applying again = %+v, %v; want no changes", again, err)
			}
		})
	}
}

func TestTemplateParametersAreChecked(t *testing.T) {
	cases := []struct {
		template string
		values   template.Values
		param    string
	}{
		{"Google mail", template.Values{"dmarc_policy": "block"}, "dmarc_policy"},
		{"Google mail", template.Values{"dmarc_rua": "not an address"}, "dmarc_rua"},
		{"Google mail", template.Values{"dmarc_rua": "a@example.com; p=none"}, "dmarc_rua"},
		{"Google mail", template.Values{"dkim_key": "not base64!"}, "dkim_key"},
		{"Google mail", template.Values{"dkim_key": "v=DKIM1; k=rsa"}, "dkim_key"},
		{"Google mail", template.Values{"dkim_selector": "bad selector", "dkim_key": "AAAA"}, "dkim_selector"},
		{"Proton mail", template.Values{"dkim1": "a.example", "dkim2": "b.example", "dkim3": "c.example"}, "verification"},
		{"Proton mail", template.Values{"verification": "bad token!", "dkim1": "a.example", "dkim2": "b.example", "dkim3": "c.example"}, "verification"},
		{"Proton mail", template.Values{"verification": "abc", "dkim2": "b.example", "dkim3": "c.example"}, "dkim1"},
		{"Proton mail", template.Values{"verification": "abc", "dkim1": "a.example", "dkim2": "not a host", "dkim3": "c.example"}, "dkim2"},
	}

	for _, tc := range cases {
		t.Run(tc.template+" "+tc.param, func(t *testing.T) {
			_, err := byName(t, tc.template).Changes(zone, nil, tc.values, 3600)

			var pe *template.ParamError
			if !errors.As(err, &pe) || pe.Param != tc.param {
				t.Fatalf("err = %v, want a ParamError for %s", err, tc.param)
			}
		})
	}
}

func TestMailTemplatesKeepTheTagsOfAnExistingDMARC(t *testing.T) {
	const hardened = "v=DMARC1; p=quarantine; sp=reject; rua=mailto:a@example.com; adkim=s; pct=50; ruf=mailto:f@example.com; fo=1"

	cases := []struct {
		name     string
		existing string
		// prefill is what the dialog starts with for the DMARC parameters.
		prefill template.Values
		// edits are made to the prefilled values.
		edits template.Values
		// want is the new DMARC value; empty when the DMARC is untouched.
		want string
		// weaker is whether the dialog warns of a weaker policy.
		weaker bool
	}{
		{
			name:     "without edits a hardened DMARC is untouched",
			existing: hardened,
			prefill:  template.Values{"dmarc_policy": "quarantine", "dmarc_rua": "mailto:a@example.com"},
		},
		{
			name:     "without edits a DMARC in another layout and quoted is untouched",
			existing: `"v=DMARC1;p=Reject;rua=mailto:a@example.com,mailto:b@example.net!10m"`,
			prefill:  template.Values{"dmarc_policy": "reject", "dmarc_rua": "mailto:a@example.com,mailto:b@example.net!10m"},
		},
		{
			name:     "a stronger policy and another report address keep the other tags",
			existing: hardened,
			prefill:  template.Values{"dmarc_policy": "quarantine", "dmarc_rua": "mailto:a@example.com"},
			edits:    template.Values{"dmarc_policy": "reject", "dmarc_rua": "b@example.com"},
			want:     "v=DMARC1; p=reject; sp=reject; rua=mailto:b@example.com; adkim=s; pct=50; ruf=mailto:f@example.com; fo=1",
		},
		{
			name:     "clearing the report address drops rua only",
			existing: hardened,
			prefill:  template.Values{"dmarc_policy": "quarantine", "dmarc_rua": "mailto:a@example.com"},
			edits:    template.Values{"dmarc_rua": ""},
			want:     "v=DMARC1; p=quarantine; sp=reject; adkim=s; pct=50; ruf=mailto:f@example.com; fo=1",
		},
		{
			name:     "a report address is added after the policy",
			existing: "v=DMARC1; p=reject; adkim=s",
			prefill:  template.Values{"dmarc_policy": "reject", "dmarc_rua": ""},
			edits:    template.Values{"dmarc_rua": "r@example.com"},
			want:     "v=DMARC1; p=reject; rua=mailto:r@example.com; adkim=s",
		},
		{
			name:     "a weaker policy is warned of",
			existing: hardened,
			prefill:  template.Values{"dmarc_policy": "quarantine", "dmarc_rua": "mailto:a@example.com"},
			edits:    template.Values{"dmarc_policy": "none"},
			want:     "v=DMARC1; p=none; sp=reject; rua=mailto:a@example.com; adkim=s; pct=50; ruf=mailto:f@example.com; fo=1",
			weaker:   true,
		},
		{
			name:    "without a DMARC the defaults are used",
			prefill: template.Values{"dmarc_policy": "none", "dmarc_rua": ""},
			want:    "v=DMARC1; p=none",
		},
	}

	for _, tc := range cases {
		for _, name := range []string{"Google mail", "Proton mail"} {
			t.Run(name+" "+tc.name, func(t *testing.T) {
				tpl := byName(t, name)

				current := []dns.Record{googleMX}
				if tc.existing != "" {
					current = append(current, rec("dm", "TXT", "_dmarc.example.com", tc.existing, 300))
				}

				values := tpl.Prefill(zone, current)
				for k, v := range tc.prefill {
					if values[k] != v {
						t.Fatalf("prefilled %s = %q, want %q", k, values[k], v)
					}
				}

				maps.Copy(values, tc.edits)

				if name == "Proton mail" {
					values["verification"], values["dkim1"], values["dkim2"], values["dkim3"] = "t", "a.example", "b.example", "c.example"
				}

				changes, err := tpl.Changes(zone, current, values, 3600)
				if err != nil {
					t.Fatal(err)
				}

				var got string

				for _, c := range changes {
					if c.Kind != dns.ChangeDelete && c.After.Name == "_dmarc.example.com" {
						got = c.After.Value
					}
				}

				if got != tc.want {
					t.Fatalf("DMARC value = %q, want %q (changes %+v)", got, tc.want, changes)
				}

				if warn := tpl.Warning(zone, current, values); (warn != "") != tc.weaker {
					t.Fatalf("Warning = %q, want a warning %v", warn, tc.weaker)
				}
			})
		}
	}
}
