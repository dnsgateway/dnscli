package godaddy_test

// Seam 2: the GoDaddy Provider against an httptest server that speaks
// the GoDaddy v1 Domains API's JSON shapes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/provider/godaddy"
)

const testToken = "test-key:test-secret"

// gdAPI is a scripted GoDaddy v1 Domains API. Domain lists are paged
// pageSize domains at a time by marker, the last domain of the page
// before; record lists pageSize records at a time by offset, the page.
type gdAPI struct {
	// pageSize is the page size the Provider asks for; zero leaves its own.
	pageSize int
	// ignorePaging answers every list with its first page, whatever the
	// offset or marker.
	ignorePaging bool
	domains      []map[string]any
	records      map[string][]map[string]any // by domain

	// reject, when it returns a message, fails the RRset write with that
	// message, as GoDaddy does for records it will not take.
	reject func(method, typ, name string, body []map[string]any) string

	mu sync.Mutex
	// writes records each RRset write.
	writes []write
	// requests records each request's URL.
	requests []*url.URL
}

// write is one RRset replace or delete the API received.
type write struct {
	Method, Path string
	Body         []map[string]any
}

// Writes returns the RRset writes received, in order.
func (api *gdAPI) Writes() []write {
	api.mu.Lock()
	defer api.mu.Unlock()

	return slices.Clone(api.writes)
}

// Requests returns each request's URL, in order.
func (api *gdAPI) Requests() []*url.URL {
	api.mu.Lock()
	defer api.mu.Unlock()

	return slices.Clone(api.requests)
}

func newProvider(t *testing.T, api *gdAPI) provider.Provider {
	t.Helper()

	srv := httptest.NewServer(api.handler(t))
	t.Cleanup(srv.Close)

	if api.pageSize > 0 {
		godaddy.SetPageSize(t, api.pageSize)
	}

	p, err := godaddy.Config{BaseURL: srv.URL}.New(
		dns.Account{ID: 1, Provider: "godaddy", Name: "work"}, testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return p
}

func (api *gdAPI) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/domains", func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if marker := r.URL.Query().Get("marker"); marker != "" && !api.ignorePaging {
			start = slices.IndexFunc(api.domains, func(d map[string]any) bool { return d["domain"] == marker }) + 1
		}

		writeJSON(w, http.StatusOK, api.domains[start:min(start+api.size(r), len(api.domains))])
	})

	mux.HandleFunc("GET /v1/domains/{domain}/records", func(w http.ResponseWriter, r *http.Request) {
		records, ok := api.records[r.PathValue("domain")]
		if !ok {
			writeError(w, http.StatusNotFound, "UNKNOWN_DOMAIN", "The given domain is not registered, or does not have a zone file")

			return
		}

		// offset is the page, from 1; GoDaddy answers 422 past the last one.
		page, err := strconv.Atoi(r.URL.Query().Get("offset"))
		if err != nil || page < 1 || api.ignorePaging {
			page = 1
		}

		size := api.size(r)
		start := (page - 1) * size

		if start > 0 && start >= len(records) {
			writeError(w, http.StatusUnprocessableEntity, "INVALID_OFFSET", "offset is past the last record")

			return
		}

		writeJSON(w, http.StatusOK, records[start:min(start+size, len(records))])
	})

	mux.HandleFunc("GET /v1/domains/{domain}/records/{type}/{name}", func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()

		var out []map[string]any
		for _, rec := range api.records[r.PathValue("domain")] {
			if rec["type"] == r.PathValue("type") && srvHost(rec) == r.PathValue("name") {
				out = append(out, rec)
			}
		}

		writeJSON(w, http.StatusOK, append([]map[string]any{}, out...))
	})
	mux.HandleFunc("PUT /v1/domains/{domain}/records/{type}/{name}", api.writeRRset)
	mux.HandleFunc("DELETE /v1/domains/{domain}/records/{type}/{name}", api.writeRRset)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		api.requests = append(api.requests, r.URL)
		api.mu.Unlock()

		if got := r.Header.Get("Authorization"); got != "sso-key "+testToken {
			t.Errorf("Authorization = %q, want the Account's key and secret", got)
		}

		mux.ServeHTTP(w, r)
	})
}

// writeRRset replaces or deletes an RRset as GoDaddy does.
func (api *gdAPI) writeRRset(w http.ResponseWriter, r *http.Request) {
	var body []map[string]any
	if r.Method == http.MethodPut {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_BODY", "Request body doesn't fulfill schema")

			return
		}
	}

	d, typ, name := r.PathValue("domain"), r.PathValue("type"), r.PathValue("name")

	api.mu.Lock()
	defer api.mu.Unlock()

	api.writes = append(api.writes, write{Method: r.Method, Path: r.URL.Path, Body: body})

	if api.reject != nil {
		if msg := api.reject(r.Method, typ, name, body); msg != "" {
			writeError(w, http.StatusUnprocessableEntity, "INVALID_BODY", msg)

			return
		}
	}

	if api.records == nil {
		api.records = map[string][]map[string]any{}
	}

	kept := slices.DeleteFunc(slices.Clone(api.records[d]), func(rec map[string]any) bool {
		return rec["type"] == typ && srvHost(rec) == name
	})

	for _, rec := range body {
		rec = maps.Clone(rec)
		rec["type"], rec["name"] = typ, name
		kept = append(kept, rec)
	}

	api.records[d] = kept

	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// size is a list's page size: the client's limit.
func (api *gdAPI) size(r *http.Request) int {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		return 100
	}

	return limit
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// srvHost is a record's name, without the _service._protocol labels an
// SRV record's name may start with: GoDaddy names an SRV RRset by its host.
func srvHost(rec map[string]any) string {
	name, _ := rec["name"].(string)
	if rec["type"] != "SRV" {
		return name
	}

	labels := strings.SplitN(name, ".", 3)
	if len(labels) < 2 || !strings.HasPrefix(labels[0], "_") || !strings.HasPrefix(labels[1], "_") {
		return name
	}

	if len(labels) == 2 {
		return "@"
	}

	return labels[2]
}

// writeError answers with a GoDaddy error body.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"code": code, "message": message})
}

func domain(name string, ns ...string) map[string]any {
	return map[string]any{"domain": name, "domainId": len(name), "status": "ACTIVE", "nameServers": ns}
}

func TestZonesAreTheAccountsActiveDomainsOnEveryPage(t *testing.T) {
	api := &gdAPI{pageSize: 2, domains: []map[string]any{
		domain("a.example"), domain("b.example"), domain("c.example"),
	}}

	zones, err := newProvider(t, api).Zones(context.Background())
	if err != nil {
		t.Fatalf("Zones: %v", err)
	}

	var names []string
	for _, z := range zones {
		names = append(names, z.Name+"="+z.ProviderID)
	}

	if want := []string{"a.example=a.example", "b.example=b.example", "c.example=c.example"}; !reflect.DeepEqual(names, want) {
		t.Errorf("Zones = %v, want %v", names, want)
	}

	for _, u := range api.Requests() {
		if got := u.Query().Get("statuses"); got != "ACTIVE" {
			t.Errorf("request %s asks for statuses %q, want ACTIVE only", u, got)
		}
	}
}

func TestZonesCarryTheirNameserversOnlyWhenGoDaddyAnswersForThem(t *testing.T) {
	api := &gdAPI{pageSize: 10, domains: []map[string]any{
		domain("hosted.example", "NS43.DOMAINCONTROL.COM", "ns44.domaincontrol.com"),
		domain("elsewhere.example", "cruz.ns.cloudflare.com", "dee.ns.cloudflare.com"),
		domain("mixed.example", "ns43.domaincontrol.com", "ns1.example.net"),
		domain("unknown.example"),
	}}

	zones, err := newProvider(t, api).Zones(context.Background())
	if err != nil {
		t.Fatalf("Zones: %v", err)
	}

	got := map[string][]string{}
	for _, z := range zones {
		got[z.Name] = z.Nameservers
	}

	want := map[string][]string{
		"hosted.example":    {"ns43.domaincontrol.com", "ns44.domaincontrol.com"},
		"elsewhere.example": nil,
		"mixed.example":     nil,
		"unknown.example":   nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Zone nameservers = %v, want %v", got, want)
	}
}

func TestNameserversAreTheGoDaddyOnesOfTheAccountsDomains(t *testing.T) {
	api := &gdAPI{pageSize: 10, domains: []map[string]any{
		domain("b.example", "ns44.domaincontrol.com", "ns43.domaincontrol.com"),
		domain("elsewhere.example", "cruz.ns.cloudflare.com", "dee.ns.cloudflare.com"),
		domain("a.example", "NS43.DOMAINCONTROL.COM.", "ns44.domaincontrol.com"),
	}}

	ns, err := newProvider(t, api).Nameservers(context.Background())
	if err != nil {
		t.Fatalf("Nameservers: %v", err)
	}

	if want := []string{"ns43.domaincontrol.com", "ns44.domaincontrol.com"}; !reflect.DeepEqual(ns, want) {
		t.Errorf("Nameservers = %v, want %v", ns, want)
	}
}

func TestRecordsListsEveryPageWithAbsoluteNamesAndThePriorityFirst(t *testing.T) {
	api := &gdAPI{pageSize: 2, records: map[string][]map[string]any{"example.com": {
		{"type": "A", "name": "@", "data": "192.0.2.1", "ttl": 600},
		{"type": "CNAME", "name": "www", "data": "@", "ttl": 3600},
		{"type": "MX", "name": "@", "data": "mail.example.com", "priority": 10, "ttl": 3600},
		{"type": "TXT", "name": "@", "data": "v=spf1 -all", "ttl": 3600},
		{"type": "SRV", "name": "@", "service": "_sip", "protocol": "_tcp", "data": "sip.example.com", "priority": 10, "weight": 5, "port": 5060, "ttl": 3600},
		{"type": "CAA", "name": "@", "data": "0 issue \"letsencrypt.org\"", "ttl": 3600},
	}}}

	set, err := newProvider(t, api).Records(context.Background(), dns.Zone{Name: "example.com", ProviderID: "example.com"})
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	want := withIDs(
		dns.Record{Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 600},
		dns.Record{Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 3600},
		dns.Record{Type: "MX", Name: "example.com", Value: "10 mail.example.com", TTL: 3600},
		dns.Record{Type: "TXT", Name: "example.com", Value: "v=spf1 -all", TTL: 3600},
		dns.Record{Type: "SRV", Name: "_sip._tcp.example.com", Value: "10 5 5060 sip.example.com", TTL: 3600},
		dns.Record{Type: "CAA", Name: "example.com", Value: "0 issue \"letsencrypt.org\"", TTL: 3600},
	)
	if !reflect.DeepEqual(set.Records, want) {
		t.Errorf("Records =\n%v\nwant\n%v", set.Records, want)
	}

	if set.Serial != 0 {
		t.Errorf("Serial = %d, want 0: GoDaddy does not report it", set.Serial)
	}
}

var example = dns.Zone{Name: "example.com", ProviderID: "example.com"}

// withIDs gives Records the Provider IDs GoDaddy's Records carry.
func withIDs(rs ...dns.Record) []dns.Record {
	out := make([]dns.Record, 0, len(rs))
	for _, r := range rs {
		out = append(out, godaddy.WithID(r))
	}

	return out
}

func TestApplyAddsARecordToItsRRsetKeepingTheOthers(t *testing.T) {
	api := &gdAPI{pageSize: 500, records: map[string][]map[string]any{"example.com": {
		{"type": "A", "name": "@", "data": "192.0.2.1", "ttl": 600},
		{"type": "A", "name": "www", "data": "192.0.2.9", "ttl": 600},
	}}}

	add := dns.Record{Type: "A", Name: "example.com", Value: "192.0.2.2", TTL: 3600}

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeAdd, After: add},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := res.Results[0]; got.Err != nil || !reflect.DeepEqual(got.Record, godaddy.WithID(add)) {
		t.Errorf("result = %+v, want the Record added, with its ID", got)
	}

	want := []write{{Method: "PUT", Path: "/v1/domains/example.com/records/A/@", Body: []map[string]any{
		{"data": "192.0.2.1", "ttl": 600.0},
		{"data": "192.0.2.2", "ttl": 3600.0},
	}}}
	if got := api.Writes(); !reflect.DeepEqual(got, want) {
		t.Errorf("writes = %v, want %v", got, want)
	}
}

func TestApplyDeletesAnRRsetItLeavesEmptyBeforeWritingTheOthers(t *testing.T) {
	api := &gdAPI{pageSize: 500, records: map[string][]map[string]any{"example.com": {
		{"type": "A", "name": "www", "data": "192.0.2.1", "ttl": 600},
	}}}

	del := dns.Record{Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 600}
	add := dns.Record{Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 600}

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeAdd, After: add},
		{Kind: dns.ChangeDelete, Before: del},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for i, r := range res.Results {
		if r.Err != nil {
			t.Errorf("result %d: %v", i, r.Err)
		}
	}

	if !reflect.DeepEqual(res.Results[1].Record, dns.Record{}) {
		t.Errorf("deletion result Record = %+v, want none", res.Results[1].Record)
	}

	want := []write{
		{Method: "DELETE", Path: "/v1/domains/example.com/records/A/www"},
		{Method: "PUT", Path: "/v1/domains/example.com/records/CNAME/www", Body: []map[string]any{{"data": "example.com", "ttl": 600.0}}},
	}
	if got := api.Writes(); !reflect.DeepEqual(got, want) {
		t.Errorf("writes = %v, want %v", got, want)
	}
}

func TestApplyMakesTwoChangesToOneRRsetInOneCall(t *testing.T) {
	api := &gdAPI{pageSize: 500, records: map[string][]map[string]any{"example.com": {
		{"type": "TXT", "name": "@", "data": "keep", "ttl": 600},
		{"type": "TXT", "name": "@", "data": "old", "ttl": 600},
		{"type": "TXT", "name": "@", "data": "gone", "ttl": 600},
	}}}

	txt := func(v string, ttl uint32) dns.Record {
		return dns.Record{Type: "TXT", Name: "example.com", Value: v, TTL: ttl}
	}

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeModify, Before: txt("old", 600), After: txt("new", 3600)},
		{Kind: dns.ChangeDelete, Before: txt("gone", 600)},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if !reflect.DeepEqual(res.Results[0].Record, godaddy.WithID(txt("new", 3600))) || res.Results[0].Err != nil || res.Results[1].Err != nil {
		t.Errorf("results = %+v", res.Results)
	}

	want := []write{{Method: "PUT", Path: "/v1/domains/example.com/records/TXT/@", Body: []map[string]any{
		{"data": "keep", "ttl": 600.0},
		{"data": "new", "ttl": 3600.0},
	}}}
	if got := api.Writes(); !reflect.DeepEqual(got, want) {
		t.Errorf("writes = %v, want %v", got, want)
	}
}

func TestApplyMovesARecordRenamedByAModification(t *testing.T) {
	api := &gdAPI{pageSize: 500, records: map[string][]map[string]any{"example.com": {
		{"type": "A", "name": "old", "data": "192.0.2.1", "ttl": 600},
	}}}

	before := dns.Record{Type: "A", Name: "old.example.com", Value: "192.0.2.1", TTL: 600}
	after := dns.Record{Type: "A", Name: "new.example.com", Value: "192.0.2.1", TTL: 600}

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeModify, Before: before, After: after},
	}})
	if err != nil || res.Results[0].Err != nil {
		t.Fatalf("Apply: %v, %+v", err, res.Results)
	}

	want := []write{
		{Method: "DELETE", Path: "/v1/domains/example.com/records/A/old"},
		{Method: "PUT", Path: "/v1/domains/example.com/records/A/new", Body: []map[string]any{{"data": "192.0.2.1", "ttl": 600.0}}},
	}
	if got := api.Writes(); !reflect.DeepEqual(got, want) {
		t.Errorf("writes = %v, want %v", got, want)
	}
}

func TestApplyKeepsGoingAfterAnRRsetFails(t *testing.T) {
	api := &gdAPI{
		pageSize: 500,
		records: map[string][]map[string]any{"example.com": {
			{"type": "A", "name": "@", "data": "192.0.2.1", "ttl": 600},
		}},
		reject: func(_, typ, _ string, _ []map[string]any) string {
			if typ == "TXT" {
				return "Record data is invalid"
			}

			return ""
		},
	}

	gone := dns.Record{Type: "A", Name: "example.com", Value: "192.0.2.7", TTL: 600}
	bad := dns.Record{Type: "TXT", Name: "example.com", Value: "x", TTL: 600}
	good := dns.Record{Type: "AAAA", Name: "example.com", Value: "2001:db8::1", TTL: 600}

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeDelete, Before: gone},
		{Kind: dns.ChangeAdd, After: bad},
		{Kind: dns.ChangeAdd, After: good},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if !errors.Is(res.Results[0].Err, provider.ErrNotFound) {
		t.Errorf("deleting a Record that is not there: err = %v, want ErrNotFound", res.Results[0].Err)
	}

	var apiErr *provider.APIError
	if !errors.As(res.Results[1].Err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity || !reflect.DeepEqual(res.Results[1].Record, dns.Record{}) {
		t.Errorf("rejected add: %+v, want a 422 APIError and no Record", res.Results[1])
	}

	if res.Results[2].Err != nil {
		t.Errorf("the other add failed: %v", res.Results[2].Err)
	}

	// The A RRset is not written: its only change failed.
	for _, w := range api.Writes() {
		if w.Path == "/v1/domains/example.com/records/A/@" {
			t.Errorf("wrote the A RRset whose deletion failed: %v", w)
		}
	}
}

func TestApplySplitsThePriorityAndSRVFieldsOutOfTheValue(t *testing.T) {
	api := &gdAPI{pageSize: 500}

	mx := dns.Record{Type: "MX", Name: "example.com", Value: "10 mail.example.com", TTL: 600}
	srv := dns.Record{Type: "SRV", Name: "_sip._tcp.example.com", Value: "10 5 5060 sip.example.com", TTL: 600}

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeAdd, After: mx},
		{Kind: dns.ChangeAdd, After: srv},
	}})
	if err != nil || res.Results[0].Err != nil || res.Results[1].Err != nil {
		t.Fatalf("Apply: %v, %+v", err, res.Results)
	}

	want := []write{
		{Method: "PUT", Path: "/v1/domains/example.com/records/MX/@", Body: []map[string]any{
			{"data": "mail.example.com", "priority": 10.0, "ttl": 600.0},
		}},
		{Method: "PUT", Path: "/v1/domains/example.com/records/SRV/@", Body: []map[string]any{
			{"data": "sip.example.com", "priority": 10.0, "weight": 5.0, "port": 5060.0, "service": "_sip", "protocol": "_tcp", "ttl": 600.0},
		}},
	}
	if got := api.Writes(); !reflect.DeepEqual(got, want) {
		t.Errorf("writes = %v, want %v", got, want)
	}

	// The records read back as they were written.
	set, err := newProvider(t, api).Records(context.Background(), example)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	if want := withIDs(mx, srv); !reflect.DeepEqual(set.Records, want) {
		t.Errorf("Records = %v, want %v", set.Records, want)
	}
}

func TestNewRefusesATokenThatIsNotKeyAndSecret(t *testing.T) {
	for _, token := range []string{"", "key-only", ":secret", "key:"} {
		if _, err := (godaddy.Config{}).New(dns.Account{Provider: "godaddy", Name: "work"}, token); err == nil {
			t.Errorf("New(%q) took a token that is not KEY:SECRET", token)
		}
	}
}

func TestGoDaddyTakesTTLsFrom600SecondsWithoutAutomatic(t *testing.T) {
	p := newProvider(t, &gdAPI{})

	if got, want := provider.TTLRangeOf(p), (dns.TTLRange{Min: 600, Max: 86400}); got != want {
		t.Errorf("TTL range = %+v, want %+v", got, want)
	}

	if provider.AutoTTLOf(p) || provider.ProxiableTypesOf(p) != nil {
		t.Error("GoDaddy has no automatic TTL and no proxy")
	}
}

func TestZoneStatusIsUnknownWithoutAsking(t *testing.T) {
	api := &gdAPI{}

	status, err := newProvider(t, api).ZoneStatus(context.Background(), example)
	if err != nil {
		t.Fatalf("ZoneStatus: %v", err)
	}

	if status != (dns.ZoneStatus{DNSSEC: dns.Unknown, HSTS: dns.Unknown}) {
		t.Errorf("ZoneStatus = %+v, want DNSSEC and HSTS unknown", status)
	}

	if reqs := api.Requests(); len(reqs) != 0 {
		t.Errorf("ZoneStatus sent %v: the v1 API reports neither setting", reqs)
	}
}

func TestErrorResponsesAreTypedErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		kind   error // nil: classified by nothing
		retry  time.Duration
		msg    string // a message the error must hold
	}{
		{"bad key", 401, `{"code":"UNABLE_TO_AUTHENTICATE","message":"Unable to authenticate"}`, provider.ErrUnauthorized, 0, "Unable to authenticate"},
		{"no API access", 403, `{"code":"ACCESS_DENIED","message":"Authenticated user is not allowed access"}`, provider.ErrUnauthorized, 0, "ACCESS_DENIED"},
		{"unknown domain", 404, `{"code":"UNKNOWN_DOMAIN","message":"The given domain is not registered"}`, provider.ErrNotFound, 0, "UNKNOWN_DOMAIN"},
		{"rate limited", 429, `{"code":"TOO_MANY_REQUESTS","message":"Too many requests","retryAfterSec":42}`, provider.ErrRateLimited, 42 * time.Second, "Too many requests"},
		{"invalid record", 422, `{"code":"INVALID_BODY","message":"Request body doesn't fulfill schema","fields":[{"code":"INVALID_VALUE","message":"data must be an IPv4 address","path":"records[0].data"}]}`, nil, 0, "records[0].data: data must be an IPv4 address"},
		{"edge page", 502, `<html>Bad Gateway</html>`, nil, 0, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			t.Cleanup(srv.Close)

			p, err := godaddy.Config{BaseURL: srv.URL}.New(dns.Account{Provider: "godaddy", Name: "work"}, testToken)
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

			if apiErr.RetryAfter != c.retry {
				t.Errorf("RetryAfter = %v, want %v", apiErr.RetryAfter, c.retry)
			}

			if !strings.Contains(err.Error(), c.msg) {
				t.Errorf("err = %q, want it to hold %q", err, c.msg)
			}
		})
	}
}

// rrset returns the domain's records of a type and name, as data/ttl pairs.
func (api *gdAPI) rrset(domain, typ, name string) []string {
	api.mu.Lock()
	defer api.mu.Unlock()

	var out []string
	for _, rec := range api.records[domain] {
		if rec["type"] == typ && rec["name"] == name {
			out = append(out, fmt.Sprintf("%v/%v", rec["data"], rec["ttl"]))
		}
	}

	return out
}

func TestApplyRestoresARenamedRecordWhoseNewRRsetFails(t *testing.T) {
	api := &gdAPI{
		records: map[string][]map[string]any{"example.com": {
			{"type": "A", "name": "www", "data": "192.0.2.1", "ttl": 600},
		}},
		reject: func(method, _, name string, _ []map[string]any) string {
			if method == http.MethodPut && name == "api" {
				return "Record name is invalid"
			}

			return ""
		},
	}

	before := dns.Record{Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 600}
	after := dns.Record{Type: "A", Name: "api.example.com", Value: "192.0.2.1", TTL: 600}

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeModify, Before: before, After: after},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if res.Results[0].Err == nil {
		t.Error("the rename was reported applied, but its new RRset was refused")
	}

	if got, want := api.rrset("example.com", "A", "www"), []string{"192.0.2.1/600"}; !reflect.DeepEqual(got, want) {
		t.Errorf("www after a failed rename = %v, want the Record back: %v", got, want)
	}
}

func TestApplyTakesBackARenamedRecordWhoseOldOneIsGone(t *testing.T) {
	api := &gdAPI{records: map[string][]map[string]any{"example.com": {
		{"type": "A", "name": "old", "data": "192.0.2.9", "ttl": 600},
	}}}

	a := func(name, ip string) dns.Record {
		return dns.Record{Type: "A", Name: name + ".example.com", Value: ip, TTL: 600}
	}

	// The new RRset is written first: the old one has an addition too,
	// so it is not left with deletions only.
	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeAdd, After: a("new", "192.0.2.8")},
		{Kind: dns.ChangeAdd, After: a("old", "192.0.2.5")},
		{Kind: dns.ChangeModify, Before: a("old", "192.0.2.1"), After: a("new", "192.0.2.1")},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if !errors.Is(res.Results[2].Err, provider.ErrNotFound) {
		t.Errorf("renaming a Record that is gone: err = %v, want ErrNotFound", res.Results[2].Err)
	}

	if got, want := api.rrset("example.com", "A", "new"), []string{"192.0.2.8/600"}; !reflect.DeepEqual(got, want) {
		t.Errorf("new after a failed rename = %v, want only the other addition: %v", got, want)
	}
}

func TestApplyDeletesTheSRVRecordOfItsOwnService(t *testing.T) {
	api := &gdAPI{records: map[string][]map[string]any{"example.com": {
		{"type": "SRV", "name": "@", "service": "_imaps", "protocol": "_tcp", "data": ".", "priority": 0, "weight": 0, "port": 0, "ttl": 3600},
		{"type": "SRV", "name": "@", "service": "_imap", "protocol": "_tcp", "data": ".", "priority": 0, "weight": 0, "port": 0, "ttl": 3600},
	}}}

	imap := dns.Record{Type: "SRV", Name: "_imap._tcp.example.com", Value: "0 0 0 .", TTL: 3600}

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeDelete, Before: imap},
	}})
	if err != nil || res.Results[0].Err != nil {
		t.Fatalf("Apply: %v, %+v", err, res.Results)
	}

	set, err := newProvider(t, api).Records(context.Background(), example)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	if want := withIDs(dns.Record{Type: "SRV", Name: "_imaps._tcp.example.com", Value: "0 0 0 .", TTL: 3600}); !reflect.DeepEqual(set.Records, want) {
		t.Errorf("Records = %v, want only _imaps left: %v", set.Records, want)
	}
}

func TestApplyKeepsTheServiceOfSRVRecordsNamedWithoutServiceFields(t *testing.T) {
	api := &gdAPI{records: map[string][]map[string]any{"example.com": {
		{"type": "SRV", "name": "_sip._tcp", "data": "sip.example.com", "priority": 10, "weight": 5, "port": 5060, "ttl": 3600},
	}}}

	add := dns.Record{Type: "SRV", Name: "_xmpp._tcp.example.com", Value: "5 0 5222 xmpp.example.com", TTL: 3600}

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeAdd, After: add},
	}})
	if err != nil || res.Results[0].Err != nil {
		t.Fatalf("Apply: %v, %+v", err, res.Results)
	}

	w := api.Writes()
	if len(w) != 1 || len(w[0].Body) != 2 || w[0].Body[0]["service"] != "_sip" || w[0].Body[0]["protocol"] != "_tcp" {
		t.Errorf("writes = %v, want the _sip._tcp Record kept with its service and protocol", w)
	}
}

func TestRecordsTurnAnAtTargetIntoTheZoneName(t *testing.T) {
	api := &gdAPI{records: map[string][]map[string]any{"example.com": {
		{"type": "MX", "name": "@", "data": "@", "priority": 10, "ttl": 3600},
		{"type": "SRV", "name": "@", "service": "_sip", "protocol": "_tcp", "data": "@", "priority": 1, "weight": 2, "port": 5060, "ttl": 3600},
	}}}

	set, err := newProvider(t, api).Records(context.Background(), example)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	want := withIDs(
		dns.Record{Type: "MX", Name: "example.com", Value: "10 example.com", TTL: 3600},
		dns.Record{Type: "SRV", Name: "_sip._tcp.example.com", Value: "1 2 5060 example.com", TTL: 3600},
	)
	if !reflect.DeepEqual(set.Records, want) {
		t.Errorf("Records = %v, want %v", set.Records, want)
	}
}

func TestRecordsStopAtAShortPage(t *testing.T) {
	var recs []map[string]any
	for i := range 3 {
		recs = append(recs, map[string]any{"type": "A", "name": fmt.Sprint("h", i), "data": "192.0.2.1", "ttl": 600})
	}

	api := &gdAPI{pageSize: 2, records: map[string][]map[string]any{"example.com": recs}}

	set, err := newProvider(t, api).Records(context.Background(), example)
	if err != nil || len(set.Records) != 3 {
		t.Fatalf("Records = %v, %v; want 3 Records", set.Records, err)
	}

	if n := len(api.Requests()); n != 2 {
		t.Errorf("sent %d requests, want 2: a page of 1 is the last", n)
	}
}

func TestListsThatIgnorePagingFailInsteadOfLoopingForever(t *testing.T) {
	api := &gdAPI{
		pageSize:     2,
		ignorePaging: true,
		domains:      []map[string]any{domain("a.example"), domain("b.example"), domain("c.example")},
		records: map[string][]map[string]any{"example.com": {
			{"type": "A", "name": "a", "data": "192.0.2.1", "ttl": 600},
			{"type": "A", "name": "b", "data": "192.0.2.1", "ttl": 600},
			{"type": "A", "name": "c", "data": "192.0.2.1", "ttl": 600},
		}},
	}

	p := newProvider(t, api)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := p.Zones(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Zones = %v, want an error about paging", err)
	}

	if _, err := p.Records(ctx, example); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Records = %v, want an error about paging", err)
	}
}

func TestZonesStopAtAShortPage(t *testing.T) {
	api := &gdAPI{pageSize: 2, domains: []map[string]any{domain("a.example"), domain("b.example"), domain("c.example")}}

	if _, err := newProvider(t, api).Zones(context.Background()); err != nil {
		t.Fatalf("Zones: %v", err)
	}

	if n := len(api.Requests()); n != 2 {
		t.Errorf("sent %d requests, want 2: a page of 1 is the last", n)
	}
}

func TestApplySaysWhenARenamedRecordCouldNotBeRestored(t *testing.T) {
	api := &gdAPI{
		records: map[string][]map[string]any{"example.com": {
			{"type": "A", "name": "www", "data": "192.0.2.1", "ttl": 600},
		}},
		// Every PUT fails: the new RRset and the restore of the old one.
		reject: func(method, _, _ string, _ []map[string]any) string {
			if method == http.MethodPut {
				return "Service unavailable"
			}

			return ""
		},
	}

	before := dns.Record{Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 600}
	after := dns.Record{Type: "A", Name: "api.example.com", Value: "192.0.2.1", TTL: 600}

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeModify, Before: before, After: after},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if msg := fmt.Sprint(res.Results[0].Err); !strings.Contains(msg, "A www.example.com 192.0.2.1 is gone") {
		t.Errorf("err = %q, want it to say the old Record is gone", msg)
	}
}

func TestRecordsCarryAProviderIDThatTellsThemApartInTheirRRset(t *testing.T) {
	api := &gdAPI{records: map[string][]map[string]any{"example.com": {
		{"type": "TXT", "name": "@", "data": "one", "ttl": 600},
		{"type": "TXT", "name": "@", "data": "two", "ttl": 600},
		{"type": "TXT", "name": "@", "data": "one", "ttl": 3600},
		{"type": "A", "name": "WWW", "data": "192.0.2.1", "ttl": 600},
	}}}

	set, err := newProvider(t, api).Records(context.Background(), example)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	seen := map[string]bool{}
	for _, r := range set.Records {
		if r.ProviderID == "" || seen[r.ProviderID] {
			t.Errorf("%v: Provider ID %q is empty or shared", r, r.ProviderID)
		}

		seen[r.ProviderID] = true
	}

	// The ID survives a change of case in the name.
	if www := set.Records[3]; www.ProviderID != godaddy.WithID(dns.Record{Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 600}).ProviderID {
		t.Errorf("www ID = %q, want it the same whatever the name's case", www.ProviderID)
	}
}
