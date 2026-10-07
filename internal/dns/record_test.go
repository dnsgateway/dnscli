package dns_test

import (
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

func TestQualifyName(t *testing.T) {
	cases := []struct {
		zone, name string
		want       string // empty when the name is refused
	}{
		{"example.com", "", "example.com"},
		{"example.com", "@", "example.com"},
		{"example.com.", " @ ", "example.com"},
		{"example.com", "www", "www.example.com"},
		{"example.com", "a.b", "a.b.example.com"},
		{"example.com", "*", "*.example.com"},
		{"example.com", "*.dev", "*.dev.example.com"},
		{"example.com", "_sip._tcp", "_sip._tcp.example.com"},
		{"example.com", "www.example.com", "www.example.com"},
		{"example.com", "example.com", "example.com"},
		{"example.com", "www.example.com.", "www.example.com"},
		{"example.com", "example.com.", "example.com"},
		{"example.com", "WWW.Example.COM.", "WWW.Example.COM"},
		{"Example.com", "www", "www.Example.com"},
		{"example.com", "www.other.org", "www.other.org.example.com"},
		{"example.com", "www.other.org.", ""},
		{"example.com", "notexample.com.", ""},
		{"example.com", "bad name", ""},
		{"example.com", "a..b", ""},
		{"example.com", "dev.*", ""},
		{"example.com", "ünicode", ""},
	}

	for _, c := range cases {
		t.Run(c.zone+" "+c.name, func(t *testing.T) {
			got, err := dns.QualifyName(c.zone, c.name)

			switch {
			case c.want == "" && err == nil:
				t.Fatalf("QualifyName(%q, %q) = %q, want an error", c.zone, c.name, got)
			case c.want != "" && (err != nil || got != c.want):
				t.Fatalf("QualifyName(%q, %q) = %q, %v, want %q", c.zone, c.name, got, err, c.want)
			}
		})
	}
}

func TestRelativeName(t *testing.T) {
	cases := []struct{ zone, name, want string }{
		{"example.com", "example.com", "@"},
		{"example.com.", "example.com.", "@"},
		{"example.com", "Example.COM", "@"},
		{"example.com", "www.example.com", "www"},
		{"Example.com", "WWW.example.COM", "WWW"},
		{"example.com", "a.b.example.com.", "a.b"},
		{"example.com", "www.other.org", "www.other.org."},
		{"example.com", "notexample.com", "notexample.com."},
	}

	for _, c := range cases {
		if got := dns.RelativeName(c.zone, c.name); got != c.want {
			t.Errorf("RelativeName(%q, %q) = %q, want %q", c.zone, c.name, got, c.want)
		}
	}
}

func TestCheckValue(t *testing.T) {
	cases := []struct {
		typ, value string
		ok         bool
	}{
		{"A", "192.0.2.1", true},
		{"A", "2001:db8::1", false},
		{"A", "192.0.2", false},
		{"A", "", false},
		{"AAAA", "2001:db8::1", true},
		{"AAAA", "192.0.2.1", false},
		{"AAAA", "::ffff:192.0.2.1", false},
		{"AAAA", "fe80::1%eth0", false},
		{"CNAME", "host.example.com", true},
		{"CNAME", "host.example.com.", true},
		{"CNAME", "two words", false},
		{"NS", "ns1.example.net", true},
		{"NS", "ns1..example.net", false},
		{"MX", "10 mail.example.com", true},
		{"MX", "0 .", true},
		{"MX", "mail.example.com", false},
		{"MX", "70000 mail.example.com", false},
		{"TXT", "v=spf1 -all", true},
		{"TXT", "   ", false},
		{"SRV", "10 5 443 host.example.com", true},
		{"SRV", "10 5 host.example.com", false},
		{"CAA", `0 issue "letsencrypt.org"`, true},
		{"CAA", "256 issue x", false},
		{"CAA", `0 is-sue "x"`, false},
		{"URI", `10 1 "https://example.com/"`, true},
		{"URI", "10 1 https://example.com/", false},
		{"URI", `10 1 ""`, false},
		{"SOA", "ns1.example.com", false},
	}

	for _, c := range cases {
		if err := dns.CheckValue(c.typ, c.value); (err == nil) != c.ok {
			t.Errorf("CheckValue(%q, %q) = %v, want ok %v", c.typ, c.value, err, c.ok)
		}
	}
}

func TestTTLRangeCheck(t *testing.T) {
	cases := []struct {
		r   dns.TTLRange
		ttl uint32
		ok  bool
	}{
		{dns.DefaultTTLRange, dns.TTLAuto, false},
		{dns.DefaultTTLRange, 30, true},
		{dns.DefaultTTLRange, 86400, true},
		{dns.DefaultTTLRange, 0, false},
		{dns.DefaultTTLRange, 29, false},
		{dns.DefaultTTLRange, 86401, false},
		{dns.TTLRange{Min: 60, Max: 86400}, 1, false},
		{dns.TTLRange{Min: 60, Max: 86400, Auto: true}, 1, true},
		{dns.TTLRange{Min: 60, Max: 86400, Auto: true}, 30, false},
		{dns.TTLRange{Min: 60, Max: 86400}, 30, false},
		{dns.TTLRange{Min: 60, Max: 86400}, 60, true},
	}

	for _, c := range cases {
		if err := c.r.Check(c.ttl); (err == nil) != c.ok {
			t.Errorf("%+v.Check(%d) = %v, want ok %v", c.r, c.ttl, err, c.ok)
		}
	}
}
