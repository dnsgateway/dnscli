package cloudflare_test

// Seam 2: the Cloudflare Provider against an httptest server that speaks
// the Cloudflare v4 API's JSON shapes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/provider/cloudflare"
)

const testToken = "test-token"

// cfAPI is a scripted Cloudflare v4 API. Lists are paginated pageSize
// items at a time, as Cloudflare does, whatever per_page the client asks for.
type cfAPI struct {
	pageSize int
	zones    []map[string]any
	records  map[string][]map[string]any // by zone ID
	dnssec   map[string]string           // DNSSEC status by zone ID
	hsts     map[string]bool             // HSTS enabled by zone ID
	// forbidden are the paths answered 403, as for a token that lacks
	// the permission.
	forbidden []string
	// fail, when set, answers every request instead of the scripted data.
	fail http.HandlerFunc
	// intercept, when set, sees each request first and answers it when it
	// returns true; the scripted data answers the others.
	intercept func(w http.ResponseWriter, r *http.Request) bool
	// writeStatus is the HTTP status of a successful record write; zero
	// means 200.
	writeStatus int
	// reject, when it returns a message, fails the record write with that
	// message, as Cloudflare does for a record it will not take.
	reject func(method string, body map[string]any) string
	nextID int

	mu sync.Mutex
	// requests records each request's path and page.
	requests []string
	// writes records each record write.
	writes []write
}

// write is one record create, update or delete the API received.
type write struct {
	Method, Path string
	Body         map[string]any
}

// Writes returns the record writes received, in order.
func (api *cfAPI) Writes() []write {
	api.mu.Lock()
	defer api.mu.Unlock()

	return slices.Clone(api.writes)
}

func (api *cfAPI) record(request string) {
	api.mu.Lock()
	defer api.mu.Unlock()

	api.requests = append(api.requests, request)
}

// Requests returns each request's path and page, in order.
func (api *cfAPI) Requests() []string {
	api.mu.Lock()
	defer api.mu.Unlock()

	return slices.Clone(api.requests)
}

func newProvider(t *testing.T, api *cfAPI) provider.Provider {
	t.Helper()

	srv := httptest.NewServer(api.handler(t))
	t.Cleanup(srv.Close)

	p, err := cloudflare.Config{BaseURL: srv.URL + "/client/v4/"}.New(
		dns.Account{ID: 1, Provider: "cloudflare", Name: "work"}, testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return p
}

func (api *cfAPI) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /client/v4/zones", func(w http.ResponseWriter, r *http.Request) {
		api.page(w, r, api.zones)
	})
	mux.HandleFunc("GET /client/v4/zones/{zone}/dns_records", func(w http.ResponseWriter, r *http.Request) {
		records, ok := api.records[r.PathValue("zone")]
		if !ok {
			writeErrors(w, http.StatusNotFound, 7003, "Could not route to /zones/x/dns_records, perhaps your object identifier is invalid?")

			return
		}

		api.page(w, r, records)
	})
	mux.HandleFunc("POST /client/v4/zones/{zone}/dns_records", api.writeRecord)
	mux.HandleFunc("PUT /client/v4/zones/{zone}/dns_records/{id}", api.writeRecord)
	mux.HandleFunc("PATCH /client/v4/zones/{zone}/dns_records/{id}", api.writeRecord)
	mux.HandleFunc("DELETE /client/v4/zones/{zone}/dns_records/{id}", api.writeRecord)
	mux.HandleFunc("GET /client/v4/zones/{zone}/dnssec", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, map[string]any{
			"status":              api.dnssec[r.PathValue("zone")],
			"algorithm":           "13",
			"digest_type":         "2",
			"flags":               257,
			"key_tag":             42,
			"modified_on":         "2024-01-02T03:04:05.000000Z",
			"dnssec_multi_signer": false,
		})
	})
	mux.HandleFunc("GET /client/v4/zones/{zone}/settings/security_header", func(w http.ResponseWriter, r *http.Request) {
		writeResult(w, map[string]any{
			"id": "security_header",
			"value": map[string]any{
				"strict_transport_security": map[string]any{
					"enabled":            api.hsts[r.PathValue("zone")],
					"max_age":            86400,
					"include_subdomains": true,
					"preload":            false,
					"nosniff":            true,
				},
			},
			"editable":    true,
			"modified_on": "2024-01-02T03:04:05.000000Z",
		})
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.record(r.URL.Path + "?page=" + r.URL.Query().Get("page"))

		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q, want the Account's API token", got)
		}

		if slices.Contains(api.forbidden, r.URL.Path) {
			writeErrors(w, http.StatusForbidden, 9109, "Unauthorized to access requested resource")

			return
		}

		if api.fail != nil {
			api.fail(w, r)

			return
		}

		if api.intercept != nil && api.intercept(w, r) {
			return
		}

		mux.ServeHTTP(w, r)
	})
}

// writeRecord creates, updates or deletes a record as Cloudflare does,
// and answers with the record as stored.
func (api *cfAPI) writeRecord(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if r.Method != http.MethodDelete {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErrors(w, http.StatusBadRequest, 9207, "Request body is invalid.")

			return
		}
	}

	api.mu.Lock()
	defer api.mu.Unlock()

	api.writes = append(api.writes, write{Method: r.Method, Path: r.URL.Path, Body: body})

	if api.reject != nil {
		if msg := api.reject(r.Method, body); msg != "" {
			writeErrors(w, http.StatusBadRequest, 9005, msg)

			return
		}
	}

	zone, id := r.PathValue("zone"), r.PathValue("id")
	if api.records == nil {
		api.records = map[string][]map[string]any{}
	}

	records := api.records[zone]
	i := slices.IndexFunc(records, func(rec map[string]any) bool { return rec["id"] == id })

	if id != "" && i < 0 {
		writeErrors(w, http.StatusNotFound, 81044, "Record does not exist.")

		return
	}

	status := api.writeStatus
	if status == 0 {
		status = http.StatusOK
	}

	var stored map[string]any

	switch r.Method {
	case http.MethodDelete:
		api.records[zone] = slices.Delete(records, i, i+1)
		writeJSON(w, status, map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": map[string]any{"id": id}})

		return
	case http.MethodPost:
		api.nextID++
		stored = storedRecord("new"+strconv.Itoa(api.nextID), nil, body)
		api.records[zone] = append(records, stored)
	case http.MethodPut:
		stored = storedRecord(id, nil, body)
		records[i] = stored
	case http.MethodPatch:
		stored = storedRecord(id, records[i], body)
		records[i] = stored
	}

	writeJSON(w, status, map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": stored})
}

// storedRecord is a record as Cloudflare stores it from a write's body,
// patched onto old when old is set. Records written with structured data
// get their content from it, as Cloudflare formats it.
func storedRecord(id string, old, body map[string]any) map[string]any {
	rec := cfRecord(id, "", "", "", 1, false, false)
	maps.Copy(rec, old)
	maps.Copy(rec, body)
	rec["id"] = id

	typ, _ := rec["type"].(string)
	rec["proxiable"] = typ == "A" || typ == "AAAA" || typ == "CNAME"

	if data, ok := body["data"].(map[string]any); ok {
		switch typ {
		case "SRV":
			rec["content"] = fmt.Sprintf("%v %v %v", data["weight"], data["port"], data["target"])
		case "URI":
			rec["content"] = fmt.Sprintf("%v %q", data["weight"], data["target"])
		case "CAA":
			rec["content"] = fmt.Sprintf("%v %v %q", data["flags"], data["tag"], data["value"])
		}
	}

	return rec
}

// page writes one page of items in the v4 list envelope.
func (api *cfAPI) page(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	size := api.pageSize
	if size == 0 {
		size = 20
	}

	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}

	start := min((page-1)*size, len(items))
	end := min(start+size, len(items))
	result := items[start:end]

	if result == nil {
		result = []map[string]any{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"errors":   []any{},
		"messages": []any{},
		"result":   result,
		"result_info": map[string]any{
			"page":        page,
			"per_page":    size,
			"count":       len(result),
			"total_count": len(items),
			"total_pages": (len(items) + size - 1) / size,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeResult writes a single result in the v4 envelope.
func writeResult(w http.ResponseWriter, result any) {
	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"errors":   []any{},
		"messages": []any{},
		"result":   result,
	})
}

func writeErrors(w http.ResponseWriter, status, code int, message string) {
	writeJSON(w, status, map[string]any{
		"success":  false,
		"errors":   []any{map[string]any{"code": code, "message": message}},
		"messages": []any{},
		"result":   nil,
	})
}

func cfZone(id, name string, ns ...string) map[string]any {
	if ns == nil {
		ns = []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}
	}

	return map[string]any{
		"id":           id,
		"name":         name,
		"status":       "active",
		"paused":       false,
		"type":         "full",
		"name_servers": ns,
		"account":      map[string]any{"id": "acc1", "name": "Work"},
		"created_on":   "2024-01-02T03:04:05.000000Z",
		"modified_on":  "2024-01-02T03:04:05.000000Z",
	}
}

func TestZonesListsEveryPage(t *testing.T) {
	api := &cfAPI{pageSize: 2, zones: []map[string]any{
		cfZone("z1", "example.com"),
		cfZone("z2", "example.net"),
		cfZone("z3", "example.org"),
	}}
	p := newProvider(t, api)

	zones, err := p.Zones(context.Background())
	if err != nil {
		t.Fatalf("Zones: %v", err)
	}

	want := []dns.Zone{
		{Name: "example.com", ProviderID: "z1", Nameservers: []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}},
		{Name: "example.net", ProviderID: "z2", Nameservers: []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}},
		{Name: "example.org", ProviderID: "z3", Nameservers: []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}},
	}
	if !reflect.DeepEqual(zones, want) {
		t.Fatalf("Zones = %+v, want %+v", zones, want)
	}

	if got := strings.Join(api.Requests(), " "); got != "/client/v4/zones?page=1 /client/v4/zones?page=2" {
		t.Errorf("requests = %s, want one per page", got)
	}
}

func TestZonesCarryTheNameserversCloudflareAssignedToEachZone(t *testing.T) {
	api := &cfAPI{zones: []map[string]any{
		cfZone("z1", "example.com", "bob.ns.cloudflare.com", "ada.ns.cloudflare.com"),
		cfZone("z2", "example.net", "cruz.ns.cloudflare.com", "dee.ns.cloudflare.com"),
	}}
	p := newProvider(t, api)

	zones, err := p.Zones(context.Background())
	if err != nil {
		t.Fatalf("Zones: %v", err)
	}

	got := map[string][]string{}
	for _, z := range zones {
		got[z.Name] = z.Nameservers
	}

	want := map[string][]string{
		"example.com": {"bob.ns.cloudflare.com", "ada.ns.cloudflare.com"},
		"example.net": {"cruz.ns.cloudflare.com", "dee.ns.cloudflare.com"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Zone nameservers = %v, want %v", got, want)
	}
}

func TestNameserversAreTheOnesCloudflareAssignedToTheAccountsZones(t *testing.T) {
	api := &cfAPI{pageSize: 1, zones: []map[string]any{
		cfZone("z1", "example.com", "bob.ns.cloudflare.com", "ada.ns.cloudflare.com"),
		cfZone("z2", "example.net", "ada.ns.cloudflare.com", "bob.ns.cloudflare.com"),
		cfZone("z3", "example.org", "cruz.ns.cloudflare.com", "ada.ns.cloudflare.com"),
	}}
	p := newProvider(t, api)

	ns, err := p.Nameservers(context.Background())
	if err != nil {
		t.Fatalf("Nameservers: %v", err)
	}

	want := []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com", "cruz.ns.cloudflare.com"}
	if !slices.Equal(ns, want) {
		t.Errorf("Nameservers = %v, want %v", ns, want)
	}
}

func TestApplyCreatesARecordWithTheProxyFlag(t *testing.T) {
	api := &cfAPI{records: map[string][]map[string]any{"z1": {}}}
	p := newProvider(t, api)

	add := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{
		Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 300, Extras: map[string]string{"proxied": "true"},
	}}

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{add}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	assertWrites(t, api, []write{{Method: "POST", Path: "/client/v4/zones/z1/dns_records", Body: map[string]any{
		"type": "A", "name": "www.example.com", "content": "192.0.2.1", "ttl": 300.0, "proxied": true,
	}}})

	assertResults(t, res, []provider.ChangeResult{{Change: add, Record: dns.Record{
		ProviderID: "new1", Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 300, Extras: map[string]string{"proxied": "true"},
	}}})
}

func TestApplyUpdatesARecordInPlaceAndTurnsTheProxyOff(t *testing.T) {
	stored := cfRecord("r1", "CNAME", "www.example.com", "example.com", 1, true, true)
	stored["comment"] = "kept by Cloudflare"
	api := &cfAPI{records: map[string][]map[string]any{"z1": {stored}}}
	p := newProvider(t, api)

	before := dns.Record{ProviderID: "r1", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 1, Extras: map[string]string{"proxied": "true"}}
	after := dns.Record{ProviderID: "r1", Type: "CNAME", Name: "www.example.com", Value: "example.net", TTL: 300, Extras: map[string]string{"proxied": "false"}}
	modify := dns.Change{Kind: dns.ChangeModify, Before: before, After: after}

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{modify}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	assertWrites(t, api, []write{{Method: "PATCH", Path: "/client/v4/zones/z1/dns_records/r1", Body: map[string]any{
		"type": "CNAME", "name": "www.example.com", "content": "example.net", "ttl": 300.0, "proxied": false,
	}}})

	assertResults(t, res, []provider.ChangeResult{{Change: modify, Record: after}})

	if got := api.records["z1"][0]["comment"]; got != "kept by Cloudflare" {
		t.Errorf("comment = %v, want the fields dnscli does not know kept", got)
	}
}

func TestApplyTurnsTheProxyOffWhenARecordChangesToATypeThatCannotHaveIt(t *testing.T) {
	// A PATCH keeps what it does not send, so a proxied CNAME patched into
	// a TXT Record would stay proxied, which Cloudflare refuses.
	api := &cfAPI{
		records: map[string][]map[string]any{"z1": {cfRecord("r1", "CNAME", "www.example.com", "example.com", 1, true, true)}},
		reject: func(_ string, body map[string]any) string {
			if body["type"] == "TXT" && body["proxied"] != false {
				return "TXT records cannot be proxied."
			}

			return ""
		},
	}
	p := newProvider(t, api)

	before := dns.Record{ProviderID: "r1", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 1, Extras: map[string]string{"proxied": "true"}}
	after := dns.Record{ProviderID: "r1", Type: "TXT", Name: "www.example.com", Value: "\"hi\"", TTL: 1}
	modify := dns.Change{Kind: dns.ChangeModify, Before: before, After: after}

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{modify}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	assertResults(t, res, []provider.ChangeResult{{Change: modify, Record: after}})
}

func TestApplyDeletesARecord(t *testing.T) {
	api := &cfAPI{records: map[string][]map[string]any{"z1": {cfRecord("r1", "TXT", "example.com", "\"old\"", 300, false, false)}}}
	p := newProvider(t, api)

	del := dns.Change{Kind: dns.ChangeDelete, Before: dns.Record{ProviderID: "r1", Type: "TXT", Name: "example.com", Value: "\"old\"", TTL: 300}}

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{del}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	assertWrites(t, api, []write{{Method: "DELETE", Path: "/client/v4/zones/z1/dns_records/r1"}})
	assertResults(t, res, []provider.ChangeResult{{Change: del}})

	if n := len(api.records["z1"]); n != 0 {
		t.Errorf("%d records left, want the Record deleted", n)
	}
}

func TestApplySendsDeletionsFirstAndReportsInChangesetOrder(t *testing.T) {
	// A CNAME replaces an A Record of the same name: deleted first, the A
	// Record does not collide with it.
	api := &cfAPI{records: map[string][]map[string]any{"z1": {
		cfRecord("r1", "A", "www.example.com", "192.0.2.1", 300, true, false),
		cfRecord("r2", "TXT", "example.com", "\"v=1\"", 300, false, false),
	}}}
	p := newProvider(t, api)

	add := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 300}}
	modify := dns.Change{Kind: dns.ChangeModify,
		Before: dns.Record{ProviderID: "r2", Type: "TXT", Name: "example.com", Value: "\"v=1\"", TTL: 300},
		After:  dns.Record{ProviderID: "r2", Type: "TXT", Name: "example.com", Value: "\"v=2\"", TTL: 300}}
	del := dns.Change{Kind: dns.ChangeDelete, Before: dns.Record{ProviderID: "r1", Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 300}}

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{add, modify, del}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var methods []string
	for _, w := range api.Writes() {
		methods = append(methods, w.Method)
	}

	if want := []string{"DELETE", "PATCH", "POST"}; !slices.Equal(methods, want) {
		t.Errorf("writes = %v, want %v", methods, want)
	}

	assertResults(t, res, []provider.ChangeResult{
		{Change: add, Record: dns.Record{ProviderID: "new1", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 300, Extras: map[string]string{"proxied": "false"}}},
		{Change: modify, Record: modify.After},
		{Change: del},
	})
}

func TestApplyKeepsGoingAfterAChangeFails(t *testing.T) {
	api := &cfAPI{
		records: map[string][]map[string]any{"z1": {}},
		reject: func(_ string, body map[string]any) string {
			if body["name"] == "bad.example.com" {
				return "Content for A record is invalid."
			}

			return ""
		},
	}
	p := newProvider(t, api)

	good1 := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "TXT", Name: "one.example.com", Value: "\"1\"", TTL: 300}}
	bad := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "A", Name: "bad.example.com", Value: "192.0.2.1", TTL: 300}}
	good2 := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "TXT", Name: "two.example.com", Value: "\"2\"", TTL: 300}}
	gone := dns.Change{Kind: dns.ChangeDelete, Before: dns.Record{ProviderID: "r9", Type: "TXT", Name: "gone.example.com", Value: "\"9\"", TTL: 300}}

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{good1, bad, good2, gone}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	assertResults(t, res, []provider.ChangeResult{
		{Change: good1, Record: dns.Record{ProviderID: "new1", Type: "TXT", Name: "one.example.com", Value: "\"1\"", TTL: 300}},
		{Change: bad, Err: errors.New("Content for A record is invalid.")},
		{Change: good2, Record: dns.Record{ProviderID: "new2", Type: "TXT", Name: "two.example.com", Value: "\"2\"", TTL: 300}},
		{Change: gone, Err: errors.New("Record does not exist.")},
	})

	if !errors.Is(res.Results[3].Err, provider.ErrNotFound) {
		t.Errorf("err = %v, want provider.ErrNotFound", res.Results[3].Err)
	}

	var apiErr *provider.APIError
	if !errors.As(res.Results[1].Err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("err = %v, want a *provider.APIError with HTTP 400", res.Results[1].Err)
	}
}

func TestApplyTakesAnySuccessful2xxResponse(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusAccepted} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			api := &cfAPI{records: map[string][]map[string]any{"z1": {}}, writeStatus: status}
			p := newProvider(t, api)

			add := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "TXT", Name: "example.com", Value: "\"hi\"", TTL: 300}}

			res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{add}})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}

			assertResults(t, res, []provider.ChangeResult{{Change: add, Record: dns.Record{
				ProviderID: "new1", Type: "TXT", Name: "example.com", Value: "\"hi\"", TTL: 300,
			}}})
		})
	}
}

func TestApplyFailsAChangeCloudflareDoesNotMarkAsSuccess(t *testing.T) {
	api := &cfAPI{fail: func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"success":  false,
			"errors":   []any{map[string]any{"code": 81057, "message": "Record already exists."}},
			"messages": []any{},
			"result":   nil,
		})
	}}
	p := newProvider(t, api)

	add := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "TXT", Name: "example.com", Value: "\"hi\"", TTL: 300}}

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{add}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	assertResults(t, res, []provider.ChangeResult{{Change: add, Err: errors.New("Record already exists. (code 81057)")}})

	var apiErr *provider.APIError
	if !errors.As(res.Results[0].Err, &apiErr) || apiErr.StatusCode != http.StatusOK {
		t.Errorf("err = %v, want a *provider.APIError with HTTP 200", res.Results[0].Err)
	}
}

func TestApplyTakesA2xxResponseWithoutASuccessField(t *testing.T) {
	api := &cfAPI{fail: func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"result": cfRecord("r1", "TXT", "example.com", `"hi"`, 300, false, false),
		})
	}}
	p := newProvider(t, api)

	add := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "TXT", Name: "example.com", Value: `"hi"`, TTL: 300}}

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{add}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	assertResults(t, res, []provider.ChangeResult{{Change: add, Record: dns.Record{
		ProviderID: "r1", Type: "TXT", Name: "example.com", Value: `"hi"`, TTL: 300,
	}}})
}

// serverError answers a request with a 5xx that asks for an immediate
// retry, as a flaky edge might.
func serverError(w http.ResponseWriter) {
	w.Header().Set("Retry-After-Ms", "0")
	writeErrors(w, http.StatusBadGateway, 10000+502, "Bad gateway")
}

func TestApplyDoesNotRetryACreate(t *testing.T) {
	var posts int

	api := &cfAPI{records: map[string][]map[string]any{"z1": {}}}
	api.intercept = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost {
			return false
		}

		api.mu.Lock()
		posts++
		first := posts == 1
		api.mu.Unlock()

		if first {
			serverError(w)

			return true
		}

		return false
	}
	p := newProvider(t, api)

	add := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "TXT", Name: "example.com", Value: `"hi"`, TTL: 300}}

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{add}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	api.mu.Lock()
	defer api.mu.Unlock()

	if posts != 1 {
		t.Errorf("got %d POSTs, want exactly 1: a create is not retried", posts)
	}

	assertResults(t, res, []provider.ChangeResult{{Change: add, Err: errors.New("HTTP 502")}})
}

func TestApplyTakesADeleteWhoseRetryFindsTheRecordGone(t *testing.T) {
	var deletes int

	api := &cfAPI{records: map[string][]map[string]any{"z1": {
		cfRecord("r1", "TXT", "example.com", `"hi"`, 300, false, false),
	}}}
	api.intercept = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodDelete {
			return false
		}

		api.mu.Lock()
		deletes++
		first := deletes == 1

		// The first DELETE is made, but its answer is lost to a 5xx.
		if first {
			api.records["z1"] = nil
		}
		api.mu.Unlock()

		if first {
			serverError(w)

			return true
		}

		return false
	}
	p := newProvider(t, api)

	del := dns.Change{Kind: dns.ChangeDelete, Before: dns.Record{ProviderID: "r1", Type: "TXT", Name: "example.com", Value: `"hi"`, TTL: 300}}

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{del}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	api.mu.Lock()
	defer api.mu.Unlock()

	if deletes != 2 {
		t.Errorf("got %d DELETEs, want 2: a delete is retried", deletes)
	}

	assertResults(t, res, []provider.ChangeResult{{Change: del}})
}

func TestApplyRejectsCAAFlagsAbove255(t *testing.T) {
	api := &cfAPI{records: map[string][]map[string]any{"z1": {}}}
	p := newProvider(t, api)

	add := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "CAA", Name: "example.com", Value: `256 issue "letsencrypt.org"`, TTL: 300}}

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: dns.Zone{Name: "example.com", ProviderID: "z1"}, Changes: []dns.Change{add}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	assertResults(t, res, []provider.ChangeResult{{Change: add, Err: errors.New("presentation form")}})
	assertWrites(t, api, nil)
}

func TestApplySplitsThePriorityAndDataOutOfTheValue(t *testing.T) {
	cases := []struct {
		typ, name, value string
		body             map[string]any
	}{
		{"MX", "example.com", "10 mail.example.com", map[string]any{
			"priority": 10.0, "content": "mail.example.com",
		}},
		{"SRV", "_sip._tcp.example.com", "20 5 5060 sip.example.com", map[string]any{
			"priority": 20.0, "data": map[string]any{"priority": 20.0, "weight": 5.0, "port": 5060.0, "target": "sip.example.com"},
		}},
		{"URI", "_ftp._tcp.example.com", `10 1 "ftp://ftp.example.com/public"`, map[string]any{
			"priority": 10.0, "data": map[string]any{"weight": 1.0, "target": "ftp://ftp.example.com/public"},
		}},
		{"CAA", "example.com", `0 issue "letsencrypt.org"`, map[string]any{
			"data": map[string]any{"flags": 0.0, "tag": "issue", "value": "letsencrypt.org"},
		}},
	}

	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			api := &cfAPI{records: map[string][]map[string]any{"z1": {}}}
			p := newProvider(t, api)
			zone := dns.Zone{Name: "example.com", ProviderID: "z1"}

			rec := dns.Record{Type: c.typ, Name: c.name, Value: c.value, TTL: 3600}
			add := dns.Change{Kind: dns.ChangeAdd, After: rec}

			res, err := p.Apply(context.Background(), dns.Changeset{Zone: zone, Changes: []dns.Change{add}})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}

			body := map[string]any{"type": c.typ, "name": c.name, "ttl": 3600.0}
			maps.Copy(body, c.body)
			assertWrites(t, api, []write{{Method: "POST", Path: "/client/v4/zones/z1/dns_records", Body: body}})

			// The stored Record, and the Records listed afterwards, have the
			// Value as it was written.
			rec.ProviderID = "new1"
			assertResults(t, res, []provider.ChangeResult{{Change: add, Record: rec}})

			rs, err := p.Records(context.Background(), zone)
			if err != nil {
				t.Fatalf("Records: %v", err)
			}

			assertRecords(t, rs.Records, []dns.Record{rec})
		})
	}
}

func assertWrites(t *testing.T, api *cfAPI, want []write) {
	t.Helper()

	if got := api.Writes(); !reflect.DeepEqual(got, want) {
		t.Errorf("writes:\n got %+v\nwant %+v", got, want)
	}
}

func assertResults(t *testing.T, res provider.ApplyResult, want []provider.ChangeResult) {
	t.Helper()

	if len(res.Results) != len(want) {
		t.Fatalf("got %d results, want %d: %+v", len(res.Results), len(want), res.Results)
	}

	for i, w := range want {
		g := res.Results[i]
		if !reflect.DeepEqual(g.Change, w.Change) {
			t.Errorf("result %d is for %+v, want %+v", i, g.Change, w.Change)
		}

		assertRecords(t, []dns.Record{g.Record}, []dns.Record{w.Record})

		if (g.Err == nil) != (w.Err == nil) || (w.Err != nil && !strings.Contains(g.Err.Error(), w.Err.Error())) {
			t.Errorf("result %d err = %v, want %v", i, g.Err, w.Err)
		}
	}
}

func TestZoneStatusReadsDNSSECAndHSTSFromTheZoneSettings(t *testing.T) {
	tests := []struct {
		dnssec string
		hsts   bool
		want   dns.ZoneStatus
	}{
		{"active", true, dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.On}},
		{"disabled", false, dns.ZoneStatus{DNSSEC: dns.Off, HSTS: dns.Off}},
		// Being signed or unsigned: neither yet.
		{"pending", false, dns.ZoneStatus{DNSSEC: dns.Pending, HSTS: dns.Off}},
		{"pending-disabled", true, dns.ZoneStatus{DNSSEC: dns.Pending, HSTS: dns.On}},
		// Cloudflare could not sign the zone: unknown, not pending.
		{"error", true, dns.ZoneStatus{DNSSEC: dns.Unknown, HSTS: dns.On}},
	}

	for _, tt := range tests {
		t.Run(tt.dnssec, func(t *testing.T) {
			api := &cfAPI{dnssec: map[string]string{"z1": tt.dnssec}, hsts: map[string]bool{"z1": tt.hsts}}
			p := newProvider(t, api)

			got, err := p.ZoneStatus(context.Background(), dns.Zone{Name: "example.com", ProviderID: "z1"})
			if err != nil {
				t.Fatalf("ZoneStatus: %v", err)
			}

			if got != tt.want {
				t.Errorf("ZoneStatus = %+v, want %+v", got, tt.want)
			}

			// The two settings are read concurrently, in either order.
			want := []string{"/client/v4/zones/z1/dnssec?page=", "/client/v4/zones/z1/settings/security_header?page="}
			if reqs := slices.Sorted(slices.Values(api.Requests())); !slices.Equal(reqs, want) {
				t.Errorf("requests = %q, want %q", reqs, want)
			}
		})
	}
}

func TestZoneStatusSettingsTheTokenMayNotReadAreUnknown(t *testing.T) {
	tests := []struct {
		name      string
		forbidden []string
		want      dns.ZoneStatus
	}{
		{"DNSSEC", []string{"/client/v4/zones/z1/dnssec"}, dns.ZoneStatus{DNSSEC: dns.Unknown, HSTS: dns.On}},
		{"HSTS", []string{"/client/v4/zones/z1/settings/security_header"}, dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.Unknown}},
		{"both", []string{"/client/v4/zones/z1/dnssec", "/client/v4/zones/z1/settings/security_header"}, dns.ZoneStatus{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &cfAPI{
				dnssec:    map[string]string{"z1": "active"},
				hsts:      map[string]bool{"z1": true},
				forbidden: tt.forbidden,
			}
			p := newProvider(t, api)

			got, err := p.ZoneStatus(context.Background(), dns.Zone{Name: "example.com", ProviderID: "z1"})
			if err != nil {
				t.Fatalf("ZoneStatus: %v", err)
			}

			if got != tt.want {
				t.Errorf("ZoneStatus = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestZoneStatusReadsDNSSECAndHSTSConcurrently(t *testing.T) {
	// The DNSSEC read is answered only once the HSTS read has arrived.
	hstsAsked := make(chan struct{})

	api := &cfAPI{dnssec: map[string]string{"z1": "active"}, hsts: map[string]bool{"z1": true}}
	h := api.handler(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/settings/security_header"):
			close(hstsAsked)
		case strings.HasSuffix(r.URL.Path, "/dnssec"):
			select {
			case <-hstsAsked:
			case <-time.After(5 * time.Second):
				writeErrors(w, http.StatusBadRequest, 1004, "HSTS was not read concurrently")

				return
			}
		}

		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	p, err := cloudflare.Config{BaseURL: srv.URL + "/client/v4/"}.New(
		dns.Account{ID: 1, Provider: "cloudflare", Name: "work"}, testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := p.ZoneStatus(context.Background(), dns.Zone{Name: "example.com", ProviderID: "z1"})
	if err != nil {
		t.Fatalf("ZoneStatus: %v", err)
	}

	if want := (dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.On}); got != want {
		t.Errorf("ZoneStatus = %+v, want %+v", got, want)
	}
}

func TestAZoneStatusSettingThatFailsToReadIsUnknownOnItsOwn(t *testing.T) {
	tests := []struct {
		name    string
		failing string
		want    dns.ZoneStatus
	}{
		{"DNSSEC", "/client/v4/zones/z1/dnssec", dns.ZoneStatus{DNSSEC: dns.Unknown, HSTS: dns.On}},
		{"HSTS", "/client/v4/zones/z1/settings/security_header", dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.Unknown}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &cfAPI{dnssec: map[string]string{"z1": "active"}, hsts: map[string]bool{"z1": true}}
			h := api.handler(t)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tt.failing {
					writeErrors(w, http.StatusBadRequest, 1004, "DNS Validation Error")

					return
				}

				h.ServeHTTP(w, r)
			}))
			t.Cleanup(srv.Close)

			p, err := cloudflare.Config{BaseURL: srv.URL + "/client/v4/"}.New(
				dns.Account{ID: 1, Provider: "cloudflare", Name: "work"}, testToken)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			got, err := p.ZoneStatus(context.Background(), dns.Zone{Name: "example.com", ProviderID: "z1"})
			if err != nil {
				t.Fatalf("ZoneStatus: %v, want the other setting read", err)
			}

			if got != tt.want {
				t.Errorf("ZoneStatus = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestZoneStatusErrorsOtherThanAMissingPermissionAreTypedErrors(t *testing.T) {
	api := &cfAPI{fail: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		writeErrors(w, http.StatusTooManyRequests, 971, "Please wait and consider throttling your request speed")
	}}
	p := newProvider(t, api)

	_, err := p.ZoneStatus(context.Background(), dns.Zone{Name: "example.com", ProviderID: "z1"})

	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("err = %v, want provider.ErrRateLimited", err)
	}
}

func TestRateLimitIsATypedErrorWithoutWaiting(t *testing.T) {
	api := &cfAPI{fail: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		writeErrors(w, http.StatusTooManyRequests, 971, "Please wait and consider throttling your request speed")
	}}
	p := newProvider(t, api)

	_, err := p.Zones(context.Background())

	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("err = %v, want provider.ErrRateLimited", err)
	}

	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T %v, want a *provider.APIError", err, err)
	}

	if apiErr.RetryAfter != 120*time.Second {
		t.Errorf("RetryAfter = %v, want the server's 2m0s", apiErr.RetryAfter)
	}

	if len(api.Requests()) != 1 {
		t.Errorf("made %d requests, want 1: a rate limit is reported, not waited out", len(api.Requests()))
	}
}

func TestEdgeRateLimitWithoutAJSONBodyIsATypedError(t *testing.T) {
	api := &cfAPI{fail: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("error code: 1015"))
	}}
	p := newProvider(t, api)

	_, err := p.Records(context.Background(), dns.Zone{Name: "example.com", ProviderID: "z1"})

	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("err = %v, want provider.ErrRateLimited", err)
	}
}

func TestErrorResponsesAreTypedErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		code    int
		message string
		is      error
	}{
		{"invalid token", http.StatusUnauthorized, 1000, "Invalid API Token", provider.ErrUnauthorized},
		{"missing permission", http.StatusForbidden, 9109, "Unauthorized to access requested resource", provider.ErrUnauthorized},
		{"unknown zone", http.StatusNotFound, 7003, "Could not route to /zones/nope/dns_records, perhaps your object identifier is invalid?", provider.ErrNotFound},
		{"server error", http.StatusInternalServerError, 10001, "Internal server error", nil},
		{"other bad request", http.StatusBadRequest, 9005, "Content for A record is invalid", nil},
		// Cloudflare answers these with HTTP 400: the error code says what went wrong.
		{"invalid request headers", http.StatusBadRequest, 6003, "Invalid request headers", provider.ErrUnauthorized},
		{"malformed Authorization header", http.StatusBadRequest, 6111, "Invalid format for Authorization header", provider.ErrUnauthorized},
		{"missing credentials", http.StatusBadRequest, 9106, "Missing X-Auth-Key, X-Auth-Email or Authorization headers", provider.ErrUnauthorized},
		{"authentication error", http.StatusBadRequest, 10000, "Authentication error", provider.ErrUnauthorized},
		{"unroutable zone ID", http.StatusBadRequest, 7003, "Could not route to /zones/nope/dns_records, perhaps your object identifier is invalid?", provider.ErrNotFound},
		{"record does not exist", http.StatusBadRequest, 81044, "Record does not exist.", provider.ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &cfAPI{fail: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "0") // keep retries of server errors quick
				writeErrors(w, tt.status, tt.code, tt.message)
			}}
			p := newProvider(t, api)

			_, err := p.Records(context.Background(), dns.Zone{Name: "example.com", ProviderID: "nope"})

			var apiErr *provider.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %T %v, want a *provider.APIError", err, err)
			}

			if apiErr.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tt.status)
			}

			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("err = %v, want it to be %v", err, tt.is)
			}

			for _, sentinel := range []error{provider.ErrRateLimited, provider.ErrUnauthorized, provider.ErrNotFound} {
				if sentinel != tt.is && errors.Is(err, sentinel) {
					t.Errorf("err = %v, should not be %v", err, sentinel)
				}
			}

			if !strings.Contains(err.Error(), tt.message) {
				t.Errorf("err = %q, want Cloudflare's message %q", err, tt.message)
			}
		})
	}
}

func TestAStalledConnectionTimesOut(t *testing.T) {
	stalled := make(chan struct{})
	t.Cleanup(func() { close(stalled) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Answer nothing, as a stalled connection does.
		select {
		case <-r.Context().Done():
		case <-stalled:
		}
	}))
	t.Cleanup(srv.Close)

	p, err := cloudflare.Config{BaseURL: srv.URL + "/client/v4/", RequestTimeout: 50 * time.Millisecond}.New(
		dns.Account{ID: 1, Provider: "cloudflare", Name: "work"}, testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan error, 1)

	go func() {
		_, err := p.Zones(context.Background())
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Zones succeeded against a stalled server")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Zones still waits on a stalled connection")
	}
}

func TestErrorsAreNotPrintedToTheTerminal(t *testing.T) {
	api := &cfAPI{fail: func(w http.ResponseWriter, _ *http.Request) {
		writeErrors(w, http.StatusForbidden, 9109, "Unauthorized to access requested resource")
	}}
	p := newProvider(t, api)

	output := captureOutput(t, func() {
		if _, err := p.Zones(context.Background()); err == nil {
			t.Error("Zones succeeded, want an error")
		}
	})

	if output != "" {
		t.Errorf("printed %q to the terminal, want nothing", output)
	}
}

// captureOutput returns what fn writes to stdout and stderr.
func captureOutput(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w

	func() {
		defer func() { os.Stdout, os.Stderr = stdout, stderr }()

		fn()
	}()

	_ = w.Close()

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

func cfRecord(id, typ, name, content string, ttl int, proxiable, proxied bool) map[string]any {
	return map[string]any{
		"id":          id,
		"type":        typ,
		"name":        name,
		"content":     content,
		"ttl":         ttl,
		"proxiable":   proxiable,
		"proxied":     proxied,
		"comment":     nil,
		"tags":        []any{},
		"settings":    map[string]any{},
		"meta":        map[string]any{},
		"created_on":  "2024-01-02T03:04:05.000000Z",
		"modified_on": "2024-01-02T03:04:05.000000Z",
	}
}

func TestRecordsListsEveryPageWithTheProxyFlag(t *testing.T) {
	api := &cfAPI{
		pageSize: 2,
		zones:    []map[string]any{cfZone("z1", "example.com")},
		records: map[string][]map[string]any{"z1": {
			cfRecord("r1", "A", "example.com", "192.0.2.1", 1, true, true),
			cfRecord("r2", "CNAME", "www.example.com", "example.com", 300, true, false),
			cfRecord("r3", "TXT", "example.com", "\"v=spf1 -all\"", 3600, false, false),
		}},
	}
	p := newProvider(t, api)

	rs, err := p.Records(context.Background(), dns.Zone{Name: "example.com", ProviderID: "z1"})
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	want := []dns.Record{
		{ProviderID: "r1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1, Extras: map[string]string{"proxied": "true"}},
		{ProviderID: "r2", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 300, Extras: map[string]string{"proxied": "false"}},
		{ProviderID: "r3", Type: "TXT", Name: "example.com", Value: "\"v=spf1 -all\"", TTL: 3600},
	}
	assertRecords(t, rs.Records, want)

	if got := strings.Join(api.Requests(), " "); !strings.Contains(got, "/client/v4/zones/z1/dns_records?page=2") {
		t.Errorf("requests = %s, want the second page of Records", got)
	}
}

func TestRecordsPutThePriorityInTheValue(t *testing.T) {
	mx := cfRecord("r1", "MX", "example.com", "mail.example.com", 3600, false, false)
	mx["priority"] = 10
	srv := cfRecord("r2", "SRV", "_sip._tcp.example.com", "5 5060 sip.example.com", 3600, false, false)
	srv["priority"] = 20
	srv["data"] = map[string]any{"priority": 20, "weight": 5, "port": 5060, "target": "sip.example.com"}

	uri := cfRecord("r3", "URI", "_ftp._tcp.example.com", `1 "ftp://ftp.example.com/public"`, 3600, false, false)
	uri["priority"] = 10
	uri["data"] = map[string]any{"weight": 1, "target": "ftp://ftp.example.com/public"}

	api := &cfAPI{records: map[string][]map[string]any{"z1": {mx, srv, uri}}}
	p := newProvider(t, api)

	rs, err := p.Records(context.Background(), dns.Zone{Name: "example.com", ProviderID: "z1"})
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	assertRecords(t, rs.Records, []dns.Record{
		{ProviderID: "r1", Type: "MX", Name: "example.com", Value: "10 mail.example.com", TTL: 3600},
		{ProviderID: "r2", Type: "SRV", Name: "_sip._tcp.example.com", Value: "20 5 5060 sip.example.com", TTL: 3600},
		{ProviderID: "r3", Type: "URI", Name: "_ftp._tcp.example.com", Value: `10 1 "ftp://ftp.example.com/public"`, TTL: 3600},
	})
}

func TestRecordsAreOneListingWithNoSerial(t *testing.T) {
	// Cloudflare's v4 API has no SOA serial short of a BIND export of the
	// whole Zone; dnscli asks the Zone's nameservers for it instead.
	api := &cfAPI{records: map[string][]map[string]any{"z1": {cfRecord("r1", "A", "example.com", "192.0.2.1", 1, true, true)}}}
	p := newProvider(t, api)

	rs, err := p.Records(context.Background(), dns.Zone{Name: "example.com", ProviderID: "z1"})
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	if rs.Serial != 0 {
		t.Errorf("Serial = %d, want 0 (unknown)", rs.Serial)
	}

	if got, want := api.Requests(), []string{"/client/v4/zones/z1/dns_records?page=1"}; !slices.Equal(got, want) {
		t.Errorf("requests = %q, want %q: one listing, no export", got, want)
	}
}

func assertRecords(t *testing.T, got, want []dns.Record) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d Records, want %d:\n got %+v\nwant %+v", len(got), len(want), got, want)
	}

	for i := range want {
		g, w := got[i], want[i]
		if g.ProviderID != w.ProviderID || g.Type != w.Type || g.Name != w.Name ||
			g.Value != w.Value || g.TTL != w.TTL || !maps.Equal(g.Extras, w.Extras) {
			t.Errorf("Record %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestCloudflareTakesTTLsFrom60Seconds(t *testing.T) {
	p, err := cloudflare.Config{}.New(dns.Account{Provider: "cloudflare", Name: "work"}, testToken)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := provider.TTLRangeOf(p), (dns.TTLRange{Min: 60, Max: 86400}); got != want {
		t.Fatalf("TTL range = %+v, want %+v", got, want)
	}
}

func TestCloudflareProxiesAAAAAAndCNAMERecords(t *testing.T) {
	p, err := cloudflare.Config{}.New(dns.Account{Provider: "cloudflare", Name: "work"}, testToken)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := provider.ProxiableTypesOf(p), []string{"A", "AAAA", "CNAME"}; !slices.Equal(got, want) {
		t.Fatalf("proxiable types = %v, want %v", got, want)
	}
}
