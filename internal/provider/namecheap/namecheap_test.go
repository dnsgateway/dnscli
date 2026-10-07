package namecheap_test

// Seam 2: the Namecheap Provider against an httptest server that speaks
// the Namecheap XML API's request parameters and response shapes.

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/provider/namecheap"
)

const testToken = "apiuser:apikey:192.0.2.10"

// ncDomain is one domain of the scripted Account.
type ncDomain struct {
	Name   string
	OurDNS bool
}

// ncHost is one host of a domain, as getHosts answers it.
type ncHost struct {
	ID, Name, Type, Address string
	MXPref, TTL             int
}

// ncError is one error of an ERROR response.
type ncError struct {
	Number int
	Text   string
}

// ncAPI is a scripted Namecheap XML API. getList pages domains PageSize at
// a time by Page, from 1; getHosts answers a domain's whole host list.
type ncAPI struct {
	domains []ncDomain
	hosts   map[string][]ncHost // by domain
	// emailType is a domain's EmailType; "MX" when not set.
	emailType map[string]string

	// servedPageSize, when set, is the page size getList serves whatever
	// PageSize asks for.
	servedPageSize int
	// noPaging leaves the Paging element out of getList answers.
	noPaging bool
	// stuckPage makes getList answer page 1 whatever Page asks for.
	stuckPage bool
	// setHostsFalse makes setHosts answer OK with IsSuccess="false" and
	// change nothing.
	setHostsFalse bool

	// fail, when it returns errors, answers the request with Status="ERROR".
	fail func(command string) []ncError

	mu sync.Mutex
	// requests records each request's parameters, query and form.
	requests []url.Values
}

// Requests returns each request's parameters, in order.
func (api *ncAPI) Requests() []url.Values {
	api.mu.Lock()
	defer api.mu.Unlock()

	return slices.Clone(api.requests)
}

// Commands returns each request's Command, in order.
func (api *ncAPI) Commands() []string {
	var out []string
	for _, r := range api.Requests() {
		out = append(out, r.Get("Command"))
	}

	return out
}

func newProvider(t *testing.T, api *ncAPI) provider.Provider {
	t.Helper()

	srv := httptest.NewServer(api.handler(t))
	t.Cleanup(srv.Close)

	p, err := namecheap.Config{BaseURL: srv.URL + "/xml.response"}.New(
		dns.Account{ID: 1, Provider: "namecheap", Name: "main"}, testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return p
}

func (api *ncAPI) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/xml.response" {
			t.Errorf("request to %s, want /xml.response", r.URL.Path)
		}

		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}

		api.mu.Lock()
		api.requests = append(api.requests, r.Form)
		api.mu.Unlock()

		cmd := r.Form.Get("Command")

		if api.fail != nil {
			if errs := api.fail(cmd); len(errs) > 0 {
				writeErrors(w, errs...)

				return
			}
		}

		switch cmd {
		case "namecheap.domains.getList":
			api.getList(w, r)
		case "namecheap.domains.dns.getHosts":
			api.getHosts(w, r)
		case "namecheap.domains.dns.setHosts":
			api.setHosts(w, r)
		default:
			writeErrors(w, ncError{1010104, "Command " + cmd + " is invalid"})
		}
	})
}

func (api *ncAPI) getList(w http.ResponseWriter, r *http.Request) {
	size, err := strconv.Atoi(r.Form.Get("PageSize"))
	if err != nil || size < 10 || size > 100 {
		size = 20
	}

	if api.servedPageSize > 0 {
		size = api.servedPageSize
	}

	page, err := strconv.Atoi(r.Form.Get("Page"))
	if err != nil || page < 1 || api.stuckPage {
		page = 1
	}

	start := min((page-1)*size, len(api.domains))

	var b strings.Builder

	b.WriteString(`<DomainGetListResult>`)

	for _, d := range api.domains[start:min(start+size, len(api.domains))] {
		fmt.Fprintf(&b, `<Domain ID="%d" Name="%s" User="apiuser" Created="02/15/2024" Expires="02/15/2027" IsExpired="false" IsLocked="false" AutoRenew="true" WhoisGuard="ENABLED" IsPremium="false" IsOurDNS="%t"/>`,
			len(d.Name), d.Name, d.OurDNS)
	}

	b.WriteString(`</DomainGetListResult>`)

	if !api.noPaging {
		fmt.Fprintf(&b, `<Paging><TotalItems>%d</TotalItems><CurrentPage>%d</CurrentPage><PageSize>%d</PageSize></Paging>`,
			len(api.domains), page, size)
	}

	writeOK(w, "namecheap.domains.getList", b.String())
}

func (api *ncAPI) getHosts(w http.ResponseWriter, r *http.Request) {
	domain := r.Form.Get("SLD") + "." + r.Form.Get("TLD")

	hosts, ok := api.hosts[domain]
	if !ok {
		writeErrors(w, ncError{2019166, "Domain not found"})

		return
	}

	var b strings.Builder

	fmt.Fprintf(&b, `<DomainDNSGetHostsResult Domain="%s" EmailType="%s" IsUsingOurDNS="true">`, domain, api.emailTypeOf(domain))

	for _, h := range hosts {
		fmt.Fprintf(&b, `<host HostId="%s" Name="%s" Type="%s" Address="%s" MXPref="%d" TTL="%d" AssociatedAppTitle="" FriendlyName="" IsActive="true" IsDDNSEnabled="false"/>`,
			h.ID, xmlEscape(h.Name), h.Type, xmlEscape(h.Address), h.MXPref, h.TTL)
	}

	b.WriteString(`</DomainDNSGetHostsResult>`)

	writeOK(w, "namecheap.domains.dns.getHosts", b.String())
}

func (api *ncAPI) emailTypeOf(domain string) string {
	if t, ok := api.emailType[domain]; ok {
		return t
	}

	return "MX"
}

// setHosts replaces a domain's hosts with the numbered HostNameN,
// RecordTypeN, AddressN, MXPrefN and TTLN form fields, from 1, giving
// each a new HostId as Namecheap does.
func (api *ncAPI) setHosts(w http.ResponseWriter, r *http.Request) {
	domain := r.Form.Get("SLD") + "." + r.Form.Get("TLD")

	if _, ok := api.hosts[domain]; !ok {
		writeErrors(w, ncError{2019166, "Domain not found"})

		return
	}

	if api.setHostsFalse {
		writeOK(w, "namecheap.domains.dns.setHosts",
			fmt.Sprintf(`<DomainDNSSetHostsResult Domain="%s" IsSuccess="false"><Warnings /></DomainDNSSetHostsResult>`, domain))

		return
	}

	hosts := sentHosts(r.Form)
	for i := range hosts {
		hosts[i].ID = strconv.Itoa(1000 + i)
	}

	api.mu.Lock()
	api.hosts[domain] = hosts

	if api.emailType == nil {
		api.emailType = map[string]string{}
	}

	api.emailType[domain] = r.Form.Get("EmailType")
	api.mu.Unlock()

	writeOK(w, "namecheap.domains.dns.setHosts",
		fmt.Sprintf(`<DomainDNSSetHostsResult Domain="%s" IsSuccess="true"><Warnings /></DomainDNSSetHostsResult>`, domain))
}

// sentHost is one host of a setHosts request, as sent. MXPref is "" when
// the request left it out.
type sentHost struct {
	Name, Type, Address, MXPref, TTL string
}

// sentHosts reads the numbered host fields of a setHosts form.
func sentHosts(form url.Values) []ncHost {
	var out []ncHost

	for _, h := range sentHostFields(form) {
		pref, _ := strconv.Atoi(h.MXPref)
		ttl, _ := strconv.Atoi(h.TTL)
		out = append(out, ncHost{Name: h.Name, Type: h.Type, Address: h.Address, MXPref: pref, TTL: ttl})
	}

	return out
}

func sentHostFields(form url.Values) []sentHost {
	var out []sentHost

	for i := 1; form.Has("HostName" + strconv.Itoa(i)); i++ {
		n := strconv.Itoa(i)
		out = append(out, sentHost{
			Name: form.Get("HostName" + n), Type: form.Get("RecordType" + n), Address: form.Get("Address" + n),
			MXPref: form.Get("MXPref" + n), TTL: form.Get("TTL" + n),
		})
	}

	return out
}

// setHostsSent returns the parameters of every setHosts request.
func (api *ncAPI) setHostsSent() []url.Values {
	var out []url.Values

	for _, r := range api.Requests() {
		if r.Get("Command") == "namecheap.domains.dns.setHosts" {
			out = append(out, r)
		}
	}

	return out
}

func xmlEscape(s string) string {
	var b strings.Builder

	_ = xml.EscapeText(&b, []byte(s))

	return b.String()
}

// writeOK answers a command with Status="OK" and its CommandResponse body.
func writeOK(w http.ResponseWriter, command, body string) {
	w.Header().Set("Content-Type", "text/xml")
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<ApiResponse Status="OK" xmlns="http://api.namecheap.com/xml.response">
  <Errors />
  <Warnings />
  <RequestedCommand>%s</RequestedCommand>
  <CommandResponse Type="%s">%s</CommandResponse>
  <Server>PHX01APIEXT01</Server>
  <GMTTimeDifference>--5:00</GMTTimeDifference>
  <ExecutionTime>0.01</ExecutionTime>
</ApiResponse>`, command, command, body)
}

// writeErrors answers with Status="ERROR" and HTTP 200, as Namecheap does.
func writeErrors(w http.ResponseWriter, errs ...ncError) {
	var b strings.Builder
	for _, e := range errs {
		fmt.Fprintf(&b, `<Error Number="%d">%s</Error>`, e.Number, xmlEscape(e.Text))
	}

	w.Header().Set("Content-Type", "text/xml")
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<ApiResponse Status="ERROR" xmlns="http://api.namecheap.com/xml.response">
  <Errors>%s</Errors>
  <Warnings />
  <RequestedCommand />
  <Server>PHX01APIEXT01</Server>
  <GMTTimeDifference>--5:00</GMTTimeDifference>
  <ExecutionTime>0.01</ExecutionTime>
</ApiResponse>`, b.String())
}

func TestNewRefusesATokenThatIsNotUserKeyAndClientIP(t *testing.T) {
	for _, token := range []string{
		"",
		"apiuser:apikey",
		":apikey:192.0.2.10",
		"apiuser::192.0.2.10",
		"apiuser:apikey:",
		"apiuser:apikey:192.0.2.10:extra",
		"apiuser:apikey:not-an-ip",
		"apiuser:apikey:2001:db8::1",
		"apiuser:apikey:::ffff:192.0.2.10",
		"apiuser:apikey:192.0.2",
	} {
		_, err := (namecheap.Config{}).New(dns.Account{Provider: "namecheap", Name: "main"}, token)
		if err == nil {
			t.Errorf("New(%q) took a token that is not APIUSER:APIKEY:CLIENTIP", token)

			continue
		}

		if !strings.Contains(err.Error(), "APIUSER:APIKEY:CLIENTIP") {
			t.Errorf("New(%q) = %q, want the error to show the token's shape", token, err)
		}
	}
}

func TestEveryRequestCarriesTheTokensAuthParameters(t *testing.T) {
	api := &ncAPI{
		domains: []ncDomain{{"example.com", true}},
		hosts:   map[string][]ncHost{"example.com": {{ID: "1", Name: "@", Type: "A", Address: "192.0.2.1", TTL: 1800}}},
	}
	p := newProvider(t, api)

	if _, err := p.Zones(context.Background()); err != nil {
		t.Fatalf("Zones: %v", err)
	}

	if _, err := p.Records(context.Background(), example); err != nil {
		t.Fatalf("Records: %v", err)
	}

	want := map[string]string{"ApiUser": "apiuser", "UserName": "apiuser", "ApiKey": "apikey", "ClientIp": "192.0.2.10"}

	reqs := api.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %v, want one getList and one getHosts", api.Commands())
	}

	for _, r := range reqs {
		for k, v := range want {
			if got := r.Get(k); got != v {
				t.Errorf("%s request: %s = %q, want %q", r.Get("Command"), k, got, v)
			}
		}
	}
}

var example = dns.Zone{Name: "example.com", ProviderID: "example.com"}

func TestZonesAreTheDomainsOnNamecheapDNSOnEveryPage(t *testing.T) {
	api := &ncAPI{}

	var want []string

	for i := range 205 {
		d := ncDomain{Name: fmt.Sprintf("d%03d.example", i), OurDNS: i%7 != 3}
		api.domains = append(api.domains, d)

		if d.OurDNS {
			want = append(want, d.Name+"="+d.Name)
		}
	}

	zones, err := newProvider(t, api).Zones(context.Background())
	if err != nil {
		t.Fatalf("Zones: %v", err)
	}

	var got []string

	for _, z := range zones {
		got = append(got, z.Name+"="+z.ProviderID)

		if z.Nameservers != nil {
			t.Errorf("Zone %s has nameservers %v, want nil", z.Name, z.Nameservers)
		}
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Zones = %v,\nwant %v", got, want)
	}

	var pages []string
	for _, r := range api.Requests() {
		pages = append(pages, r.Get("Command")+" page "+r.Get("Page")+" of "+r.Get("PageSize"))
	}

	wantPages := []string{
		"namecheap.domains.getList page 1 of 100",
		"namecheap.domains.getList page 2 of 100",
		"namecheap.domains.getList page 3 of 100",
	}
	if !reflect.DeepEqual(pages, wantPages) {
		t.Errorf("requests = %v, want %v", pages, wantPages)
	}
}

// domains returns n domains on Namecheap DNS, and their names.
func domains(n int) ([]ncDomain, []string) {
	var (
		ds    []ncDomain
		names []string
	)

	for i := range n {
		ds = append(ds, ncDomain{Name: fmt.Sprintf("d%03d.example", i), OurDNS: true})
		names = append(names, ds[i].Name)
	}

	return ds, names
}

func zoneNames(zones []dns.Zone) []string {
	var out []string
	for _, z := range zones {
		out = append(out, z.Name)
	}

	return out
}

func TestZonesReadEveryPageWhenTheAnswerHasNoTotal(t *testing.T) {
	api := &ncAPI{noPaging: true}

	var want []string
	api.domains, want = domains(150)

	zones, err := newProvider(t, api).Zones(context.Background())
	if err != nil {
		t.Fatalf("Zones: %v", err)
	}

	if got := zoneNames(zones); !reflect.DeepEqual(got, want) {
		t.Errorf("Zones = %d zones, want %d", len(got), len(want))
	}
}

func TestZonesReadEveryPageWhenNamecheapServesSmallerPages(t *testing.T) {
	api := &ncAPI{servedPageSize: 20}

	var want []string
	api.domains, want = domains(45)

	zones, err := newProvider(t, api).Zones(context.Background())
	if err != nil {
		t.Fatalf("Zones: %v", err)
	}

	if got := zoneNames(zones); !reflect.DeepEqual(got, want) {
		t.Errorf("Zones = %d zones, want %d", len(got), len(want))
	}
}

func TestZonesFailWhenTheListAnswersTheSamePageTwice(t *testing.T) {
	api := &ncAPI{stuckPage: true}
	api.domains, _ = domains(250)

	_, err := newProvider(t, api).Zones(context.Background())
	if err == nil || !strings.Contains(err.Error(), "same page") {
		t.Errorf("err = %v, want the same page answered twice", err)
	}

	if got := len(api.Commands()); got != 2 {
		t.Errorf("requests = %d, want 2: stop at the repeated page", got)
	}
}

func TestZonesOfAnAccountWithNoDomainsAreNone(t *testing.T) {
	api := &ncAPI{}

	zones, err := newProvider(t, api).Zones(context.Background())
	if err != nil {
		t.Fatalf("Zones: %v", err)
	}

	if len(zones) != 0 {
		t.Errorf("Zones = %v, want none", zones)
	}

	if got := api.Commands(); len(got) != 1 {
		t.Errorf("requests = %v, want one getList", got)
	}
}

func TestRecordsAddressTheDomainBySLDAndTLD(t *testing.T) {
	for _, c := range []struct{ zone, sld, tld string }{
		{"example.com", "example", "com"},
		{"example.co.uk", "example", "co.uk"},
	} {
		api := &ncAPI{hosts: map[string][]ncHost{c.zone: nil}}

		if _, err := newProvider(t, api).Records(context.Background(), dns.Zone{Name: c.zone, ProviderID: c.zone}); err != nil {
			t.Fatalf("Records(%s): %v", c.zone, err)
		}

		r := api.Requests()[0]
		if r.Get("Command") != "namecheap.domains.dns.getHosts" || r.Get("SLD") != c.sld || r.Get("TLD") != c.tld {
			t.Errorf("Records(%s) sent %s SLD=%q TLD=%q, want getHosts SLD=%q TLD=%q",
				c.zone, r.Get("Command"), r.Get("SLD"), r.Get("TLD"), c.sld, c.tld)
		}
	}
}

func TestRecordsHaveAbsoluteNamesThePriorityFirstAndTheirContentAsID(t *testing.T) {
	api := &ncAPI{hosts: map[string][]ncHost{"example.com": {
		{ID: "11", Name: "@", Type: "A", Address: "192.0.2.1", TTL: 1799},
		{ID: "12", Name: "www", Type: "CNAME", Address: "example.com.", TTL: 1800},
		{ID: "13", Name: "@", Type: "MX", Address: "mx1.example.net.", MXPref: 10, TTL: 3600},
		{ID: "14", Name: "@", Type: "TXT", Address: "v=spf1 include:_spf.example.net ~all", TTL: 1799},
		{ID: "15", Name: "sub", Type: "NS", Address: "ns1.example.net.", TTL: 1800},
		{ID: "16", Name: "@", Type: "CAA", Address: `0 issue "letsencrypt.org"`, TTL: 1800},
		{ID: "17", Name: "*.dev", Type: "AAAA", Address: "2001:db8::1", TTL: 60},
		{ID: "18", Name: "go", Type: "URL", Address: "https://example.org/", TTL: 1800},
		{ID: "19", Name: "old", Type: "URL301", Address: "https://example.org/new", TTL: 1800},
		{ID: "20", Name: "frame", Type: "FRAME", Address: "https://example.org/", TTL: 1800},
		{ID: "21", Name: "alias", Type: "ALIAS", Address: "target.example.net.", TTL: 300},
		{ID: "22", Name: "@", Type: "MXE", Address: "192.0.2.25", TTL: 1800},
		{ID: "23", Name: "Shop", Type: "A", Address: "192.0.2.3", TTL: 300},
	}}}

	set, err := newProvider(t, api).Records(context.Background(), example)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	want := []dns.Record{
		{ProviderID: "A example.com 1799 192.0.2.1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1799},
		{ProviderID: "CNAME www.example.com 1800 example.com", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 1800},
		{ProviderID: "MX example.com 3600 10 mx1.example.net", Type: "MX", Name: "example.com", Value: "10 mx1.example.net", TTL: 3600},
		{ProviderID: "TXT example.com 1799 v=spf1 include:_spf.example.net ~all", Type: "TXT", Name: "example.com", Value: "v=spf1 include:_spf.example.net ~all", TTL: 1799},
		{ProviderID: "NS sub.example.com 1800 ns1.example.net", Type: "NS", Name: "sub.example.com", Value: "ns1.example.net", TTL: 1800},
		{ProviderID: `CAA example.com 1800 0 issue "letsencrypt.org"`, Type: "CAA", Name: "example.com", Value: `0 issue "letsencrypt.org"`, TTL: 1800},
		{ProviderID: "AAAA *.dev.example.com 60 2001:db8::1", Type: "AAAA", Name: "*.dev.example.com", Value: "2001:db8::1", TTL: 60},
		{ProviderID: "URL go.example.com 1800 https://example.org/", Type: "URL", Name: "go.example.com", Value: "https://example.org/", TTL: 1800},
		{ProviderID: "URL301 old.example.com 1800 https://example.org/new", Type: "URL301", Name: "old.example.com", Value: "https://example.org/new", TTL: 1800},
		{ProviderID: "FRAME frame.example.com 1800 https://example.org/", Type: "FRAME", Name: "frame.example.com", Value: "https://example.org/", TTL: 1800},
		{ProviderID: "ALIAS alias.example.com 300 target.example.net", Type: "ALIAS", Name: "alias.example.com", Value: "target.example.net", TTL: 300},
		{ProviderID: "MXE example.com 1800 192.0.2.25", Type: "MXE", Name: "example.com", Value: "192.0.2.25", TTL: 1800},
		{ProviderID: "A shop.example.com 300 192.0.2.3", Type: "A", Name: "Shop.example.com", Value: "192.0.2.3", TTL: 300},
	}
	if !reflect.DeepEqual(set.Records, want) {
		t.Errorf("Records =\n%v\nwant\n%v", set.Records, want)
	}

	if set.Serial != 0 {
		t.Errorf("Serial = %d, want 0: Namecheap does not report it", set.Serial)
	}
}

func TestRecordsKeepTheRootTargetAsADot(t *testing.T) {
	api := &ncAPI{hosts: map[string][]ncHost{"example.com": {
		{ID: "31", Name: "@", Type: "MX", Address: ".", MXPref: 0, TTL: 1800},
		{ID: "32", Name: "nil", Type: "CNAME", Address: ".", TTL: 1800},
		{ID: "33", Name: "sub", Type: "NS", Address: ".", TTL: 1800},
		{ID: "34", Name: "alias", Type: "ALIAS", Address: ".", TTL: 300},
	}}}

	set, err := newProvider(t, api).Records(context.Background(), example)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	var got []string
	for _, r := range set.Records {
		got = append(got, r.Type+" "+r.Value)
	}

	if want := []string{"MX 0 .", "CNAME .", "NS .", "ALIAS ."}; !reflect.DeepEqual(got, want) {
		t.Errorf("Records = %q, want %q", got, want)
	}
}

func TestSRVRecordsPutTheMXPrefPriorityFirst(t *testing.T) {
	api := &ncAPI{hosts: map[string][]ncHost{"example.com": {
		{ID: "41", Name: "_sip._tcp", Type: "SRV", Address: "5 5060 sip.example.net.", MXPref: 10, TTL: 1800},
		{ID: "42", Name: "_xmpp._tcp.chat", Type: "SRV", Address: "0 5222 xmpp.example.net", MXPref: 20, TTL: 1800},
		{ID: "43", Name: "_none._tcp", Type: "SRV", Address: "0 0 .", MXPref: 0, TTL: 1800},
	}}}

	set, err := newProvider(t, api).Records(context.Background(), example)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	want := []dns.Record{
		{ProviderID: "SRV _sip._tcp.example.com 1800 10 5 5060 sip.example.net", Type: "SRV", Name: "_sip._tcp.example.com", Value: "10 5 5060 sip.example.net", TTL: 1800},
		{ProviderID: "SRV _xmpp._tcp.chat.example.com 1800 20 0 5222 xmpp.example.net", Type: "SRV", Name: "_xmpp._tcp.chat.example.com", Value: "20 0 5222 xmpp.example.net", TTL: 1800},
		{ProviderID: "SRV _none._tcp.example.com 1800 0 0 0 .", Type: "SRV", Name: "_none._tcp.example.com", Value: "0 0 0 .", TTL: 1800},
	}
	if !reflect.DeepEqual(set.Records, want) {
		t.Errorf("Records =\n%v\nwant\n%v", set.Records, want)
	}
}

func TestRecordsRefuseAnAnswerOverTheSizeLimit(t *testing.T) {
	huge := strings.Repeat("a", 5<<20)
	api := &ncAPI{hosts: map[string][]ncHost{"example.com": {
		{ID: "51", Name: "@", Type: "TXT", Address: huge, TTL: 1800},
	}}}

	set, err := newProvider(t, api).Records(context.Background(), example)
	if err == nil {
		t.Fatalf("Records = %d records, want an error: the answer is over 4 MiB", len(set.Records))
	}

	if !strings.Contains(err.Error(), "namecheap") {
		t.Errorf("err = %v, want it to name namecheap", err)
	}
}

func TestErrorAnswersAreTypedErrorsWithTheirNumbers(t *testing.T) {
	cases := []struct {
		name string
		errs []ncError
		kind error // nil: classified by nothing
		msg  string
	}{
		{"bad key", []ncError{{1011102, "Parameter APIKey is invalid"}}, provider.ErrUnauthorized, "1011102: Parameter APIKey is invalid"},
		{"bad user", []ncError{{1011100, "Parameter APIUser is invalid"}}, provider.ErrUnauthorized, "1011100: Parameter APIUser is invalid"},
		{"API access disabled", []ncError{{1017101, "API access has not been enabled"}}, provider.ErrUnauthorized, "1017101: API access has not been enabled"},
		{"IP not whitelisted", []ncError{{1011150, "Invalid request IP: 198.51.100.7"}}, provider.ErrUnauthorized, "1011150: Invalid request IP: 198.51.100.7"},
		{"unknown domain", []ncError{{2019166, "Domain not found"}}, provider.ErrNotFound, "2019166: Domain not found"},
		{"not the Account's domain", []ncError{{2016166, "Domain is not associated with your account"}}, provider.ErrNotFound, "2016166: Domain is not associated with your account"},
		{"rate limited", []ncError{{500000, "Too many requests"}}, provider.ErrRateLimited, "500000: Too many requests"},
		{"domain not on Namecheap DNS", []ncError{{2030288, "Cannot complete this command as this domain is not using proper DNS servers"}}, provider.ErrNotFound, "2030288: Cannot complete this command"},
		{"unknown number, too many requests", []ncError{{9999901, "Too many requests"}}, provider.ErrRateLimited, "9999901: Too many requests"},
		{"unknown number, IP not whitelisted", []ncError{{9999902, "Invalid request IP: 198.51.100.7"}}, provider.ErrUnauthorized, "9999902: Invalid request IP"},
		{"unknown number, IP not on the whitelist", []ncError{{9999903, "Your IP is not whitelisted"}}, provider.ErrUnauthorized, "9999903: Your IP is not whitelisted"},
		{"unknown number, bad API key", []ncError{{9999904, "API Key is invalid or API access has not been enabled"}}, provider.ErrUnauthorized, "9999904: API Key is invalid"},
		{"other", []ncError{{3050900, "Unknown error"}, {2030280, "TLD is not supported"}}, nil, "3050900: Unknown error; 2030280: TLD is not supported"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &ncAPI{
				hosts: map[string][]ncHost{"example.com": nil},
				fail:  func(string) []ncError { return c.errs },
			}

			_, err := newProvider(t, api).Records(context.Background(), example)

			var apiErr *provider.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want an APIError", err)
			}

			for _, kind := range []error{provider.ErrUnauthorized, provider.ErrNotFound, provider.ErrRateLimited} {
				if errors.Is(err, kind) != (kind == c.kind) {
					t.Errorf("errors.Is(%v, %v) = %v", err, kind, kind != c.kind)
				}
			}

			if apiErr.RetryAfter != 0 {
				t.Errorf("RetryAfter = %v, want zero: Namecheap does not say", apiErr.RetryAfter)
			}

			if !strings.Contains(err.Error(), c.msg) {
				t.Errorf("err = %q, want it to hold %q", err, c.msg)
			}

			if strings.Contains(err.Error(), "HTTP") {
				t.Errorf("err = %q, want no HTTP status: the error came in the answer", err)
			}
		})
	}
}

func TestHTTPErrorStatusesAreClassifiedByStatus(t *testing.T) {
	cases := []struct {
		status int
		kind   error
	}{
		{http.StatusTooManyRequests, provider.ErrRateLimited},
		{http.StatusUnauthorized, provider.ErrUnauthorized},
		{http.StatusBadGateway, nil},
	}

	for _, c := range cases {
		t.Run(strconv.Itoa(c.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(`<html>error</html>`))
			}))
			t.Cleanup(srv.Close)

			p, err := namecheap.Config{BaseURL: srv.URL}.New(dns.Account{Provider: "namecheap", Name: "main"}, testToken)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			_, err = p.Zones(context.Background())

			var apiErr *provider.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != c.status {
				t.Fatalf("err = %v, want an APIError with HTTP %d", err, c.status)
			}

			for _, kind := range []error{provider.ErrUnauthorized, provider.ErrNotFound, provider.ErrRateLimited} {
				if errors.Is(err, kind) != (kind == c.kind) {
					t.Errorf("errors.Is(%v, %v) = %v", err, kind, kind != c.kind)
				}
			}

			if apiErr.RetryAfter != 0 {
				t.Errorf("RetryAfter = %v, want zero", apiErr.RetryAfter)
			}
		})
	}
}

func TestErrorsNeverCarryTheAPIKey(t *testing.T) {
	api := &ncAPI{fail: func(string) []ncError { return []ncError{{1011102, "Parameter APIKey is invalid"}} }}

	_, err := newProvider(t, api).Zones(context.Background())
	if err == nil || strings.Contains(err.Error(), "apikey") {
		t.Errorf("err = %v, want an error without the API key", err)
	}

	p, err := namecheap.Config{BaseURL: "http://127.0.0.1:1/xml.response"}.New(dns.Account{Provider: "namecheap", Name: "main"}, testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := p.Zones(context.Background()); err == nil || strings.Contains(err.Error(), "apikey") {
		t.Errorf("unreachable API: err = %v, want an error without the API key", err)
	}
}

func TestNameserversAreNamecheapsDNSSortedWithoutAsking(t *testing.T) {
	api := &ncAPI{}

	ns, err := newProvider(t, api).Nameservers(context.Background())
	if err != nil {
		t.Fatalf("Nameservers: %v", err)
	}

	if want := []string{"dns1.registrar-servers.com", "dns2.registrar-servers.com"}; !reflect.DeepEqual(ns, want) {
		t.Errorf("Nameservers = %v, want %v", ns, want)
	}

	if got := api.Commands(); len(got) != 0 {
		t.Errorf("Nameservers sent %v, want no request", got)
	}
}

func TestZoneStatusIsUnknownWithoutAsking(t *testing.T) {
	api := &ncAPI{}

	status, err := newProvider(t, api).ZoneStatus(context.Background(), example)
	if err != nil {
		t.Fatalf("ZoneStatus: %v", err)
	}

	if status != (dns.ZoneStatus{DNSSEC: dns.Unknown, HSTS: dns.Unknown}) {
		t.Errorf("ZoneStatus = %+v, want DNSSEC and HSTS unknown", status)
	}

	if got := api.Commands(); len(got) != 0 {
		t.Errorf("ZoneStatus sent %v: getHosts reports neither setting", got)
	}
}

func TestNamecheapTakesTTLsFrom60To60000WithoutAutomaticOrProxy(t *testing.T) {
	p := newProvider(t, &ncAPI{})

	if got, want := provider.TTLRangeOf(p), (dns.TTLRange{Min: 60, Max: 60000}); got != want {
		t.Errorf("TTL range = %+v, want %+v", got, want)
	}

	if provider.AutoTTLOf(p) || provider.ProxiableTypesOf(p) != nil {
		t.Error("Namecheap has no automatic TTL and no proxy")
	}
}
