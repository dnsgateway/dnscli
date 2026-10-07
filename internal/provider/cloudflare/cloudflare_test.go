package cloudflare_test

// Seam 2: the Cloudflare Provider against an httptest server that speaks
// the Cloudflare v4 API's JSON shapes.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

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
	// fail, when set, answers every request instead of the scripted data.
	fail http.HandlerFunc
	// requests records each request's path and page.
	requests []string
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.requests = append(api.requests, r.URL.Path+"?page="+r.URL.Query().Get("page"))

		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q, want the Account's API token", got)
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
		{Name: "example.com", ProviderID: "z1"},
		{Name: "example.net", ProviderID: "z2"},
		{Name: "example.org", ProviderID: "z3"},
	}
	if !slices.Equal(zones, want) {
		t.Fatalf("Zones = %+v, want %+v", zones, want)
	}

	if got := strings.Join(api.requests, " "); got != "/client/v4/zones?page=1 /client/v4/zones?page=2" {
		t.Errorf("requests = %s, want one per page", got)
	}
}
