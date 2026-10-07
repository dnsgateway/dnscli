package cloudflare_test

// Seam 2: the Cloudflare Provider against an httptest server that speaks
// the Cloudflare v4 API's JSON shapes.

import (
	"context"
	"encoding/json"
	"errors"
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
	export   map[string]string           // BIND export by zone ID
	dnssec   map[string]string           // DNSSEC status by zone ID
	hsts     map[string]bool             // HSTS enabled by zone ID
	// forbidden are the paths answered 403, as for a token that lacks
	// the permission.
	forbidden []string
	// fail, when set, answers every request instead of the scripted data.
	fail http.HandlerFunc

	mu sync.Mutex
	// requests records each request's path and page.
	requests []string
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
	mux.HandleFunc("GET /client/v4/zones/{zone}/dns_records/export", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(api.export[r.PathValue("zone")]))
	})
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

		mux.ServeHTTP(w, r)
	})
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

func TestApplyIsNotImplementedYet(t *testing.T) {
	api := &cfAPI{}
	p := newProvider(t, api)
	zone := dns.Zone{Name: "example.com", ProviderID: "z1"}

	if _, err := p.Apply(context.Background(), dns.Changeset{Zone: zone}); !errors.Is(err, provider.ErrNotImplemented) {
		t.Errorf("Apply err = %v, want provider.ErrNotImplemented", err)
	}

	if n := len(api.Requests()); n != 0 {
		t.Errorf("made %d requests, want none", n)
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

// exportExample is a zone export as Cloudflare formats it.
const exportExample = `;;
;; Domain:     example.com.
;; Exported:   2026-10-07 12:00:00
;;
;; This file is intended for use for informational and archival
;; purposes ONLY and MUST be edited before use on a production
;; DNS server.
;;

;; SOA Record
example.com	3600	IN	SOA	ada.ns.cloudflare.com. dns.cloudflare.com. 2051234567 10000 2400 604800 3600

;; NS Records
example.com.	86400	IN	NS	ada.ns.cloudflare.com.
example.com.	86400	IN	NS	bob.ns.cloudflare.com.

;; A Records
example.com.	1	IN	A	192.0.2.1 ; cf_tags=cf-proxied:true
`

func TestRecordsReadTheSerialFromTheZoneExport(t *testing.T) {
	api := &cfAPI{
		records: map[string][]map[string]any{"z1": {cfRecord("r1", "A", "example.com", "192.0.2.1", 1, true, true)}},
		export:  map[string]string{"z1": exportExample},
	}
	p := newProvider(t, api)

	rs, err := p.Records(context.Background(), dns.Zone{Name: "example.com", ProviderID: "z1"})
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	if rs.Serial != 2051234567 {
		t.Errorf("Serial = %d, want the export's SOA serial 2051234567", rs.Serial)
	}
}

func TestRecordsWithoutAnSOAInTheExportHaveNoSerial(t *testing.T) {
	api := &cfAPI{
		records: map[string][]map[string]any{"z1": {}},
		export:  map[string]string{"z1": ";; no SOA here\n"},
	}
	p := newProvider(t, api)

	rs, err := p.Records(context.Background(), dns.Zone{Name: "example.com", ProviderID: "z1"})
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	if rs.Serial != 0 {
		t.Errorf("Serial = %d, want 0 (unknown)", rs.Serial)
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
