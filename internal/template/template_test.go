package template_test

import (
	"errors"
	"fmt"
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
