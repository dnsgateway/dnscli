package provider_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providers"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/provider"
)

// authServer answers every request 401 and keeps how each was
// authenticated: the Authorization header, or the Namecheap parameters.
type authServer struct {
	mu   sync.Mutex
	auth []string
}

func (s *authServer) start(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()

		got := r.Header.Get("Authorization")
		if u := r.Form.Get("ApiUser"); u != "" {
			got = u + " " + r.Form.Get("ApiKey") + " " + r.Form.Get("ClientIp")
		}

		s.mu.Lock()
		s.auth = append(s.auth, got)
		s.mu.Unlock()

		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func TestOldSingleTokensBuildWorkingProviders(t *testing.T) {
	for _, tc := range []struct {
		typ, token, path, want string
	}{
		{"cloudflare", "cf-token", "/client/v4/", "Bearer cf-token"},
		{"godaddy", "gd-key:gd-secret", "", "sso-key gd-key:gd-secret"},
		{"namecheap", "apiuser:apikey:192.0.2.10", "", "apiuser apikey 192.0.2.10"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			srv := &authServer{}
			base := srv.start(t) + tc.path

			// Cloudflare lists the Tenants the token reaches while it is
			// opened; the others are first called with Account.
			p, err := provider.Open(context.Background(), providers.Registry(), tc.typ, tc.token, dnsprovider.WithBaseURL(base))
			if err == nil {
				_, err = p.Account(context.Background())
			}

			// The test server refuses every token: the Provider must have
			// sent this one, as the Provider takes it.
			if !errors.Is(err, dnsprovider.ErrUnauthorized) {
				t.Errorf("error = %v, want ErrUnauthorized", err)
			}

			if len(srv.auth) == 0 || srv.auth[0] != tc.want {
				t.Errorf("sent credentials %q, want %q first", srv.auth, tc.want)
			}
		})
	}
}

func TestMalformedOldTokensAreRefusedWithTheirShape(t *testing.T) {
	for _, tc := range []struct{ typ, token, want string }{
		{"godaddy", "only-a-key", "KEY:SECRET"},
		{"godaddy", ":secret", "KEY:SECRET"},
		{"namecheap", "user:key", "APIUSER:APIKEY:CLIENTIP"},
		{"namecheap", "user::192.0.2.10", "APIUSER:APIKEY:CLIENTIP"},
		{"namecheap", "user:key:2001:db8::1", "APIUSER:APIKEY:CLIENTIP"},
		{"namecheap", "user:key:not-an-ip", "IPv4"},
		{"cloudflare", "", "token"},
	} {
		_, err := provider.Open(context.Background(), providers.Registry(), tc.typ, tc.token)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Open(%s, %q) error = %v, want one naming %s", tc.typ, tc.token, err, tc.want)
		}
	}
}

func TestOpenRefusesAnUnknownProviderType(t *testing.T) {
	if _, err := provider.Open(context.Background(), providers.Registry(), "route53", "tok"); err == nil {
		t.Error("Open(route53) succeeded, want an error")
	}
}

// tenantRegistry is a Registry whose "tenanted" Provider takes a Tenant
// field, as Cloudflare does, and builds the fake; built records the
// Tenant of each build.
func tenantRegistry(f *providertest.Provider, built *[]string) dnsprovider.Registry {
	inner := f.Factory()

	return dnsprovider.Registry{"tenanted": {
		Fields: append(inner.Fields, dnsprovider.Field{Name: "tenant_id", Label: "Account ID"}),
		New: func(m map[string]string, opts ...dnsprovider.Option) (dnsprovider.Provider, error) {
			*built = append(*built, m["tenant_id"])

			return inner.New(map[string]string{"token": m["token"]}, opts...)
		},
	}}
}

func TestATokenReachingOneTenantUsesIt(t *testing.T) {
	f := providertest.New()
	f.SetTenants(dnsprovider.Tenant{ID: "0123456789abcdef0123456789abcdef", Name: "Acme Ltd"})

	var built []string

	if _, err := provider.Open(context.Background(), tenantRegistry(f, &built), "tenanted", "one-tenant-token"); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if n := len(built); n == 0 || built[n-1] != "0123456789abcdef0123456789abcdef" {
		t.Errorf("built with Tenants %q, want the only one last", built)
	}
}

func TestATokenReachingSeveralTenantsFailsClearly(t *testing.T) {
	f := providertest.New()
	f.SetTenants(dnsprovider.Tenant{ID: "a1", Name: "Acme Ltd"}, dnsprovider.Tenant{ID: "b2", Name: "Side project"})

	var built []string

	_, err := provider.Open(context.Background(), tenantRegistry(f, &built), "tenanted", "two-tenant-token")
	if err == nil || !strings.Contains(err.Error(), "Acme Ltd") || !strings.Contains(err.Error(), "Side project") {
		t.Errorf("Open error = %v, want one naming both Tenants", err)
	}
}

func TestATokenReachingNoTenantFails(t *testing.T) {
	f := providertest.New()
	f.SetTenants()

	var built []string

	if _, err := provider.Open(context.Background(), tenantRegistry(f, &built), "tenanted", "no-tenant-token"); err == nil {
		t.Error("Open succeeded, want an error")
	}
}

func TestATenantListFailureKeepsItsKind(t *testing.T) {
	f := providertest.New()
	f.SetTenants(dnsprovider.Tenant{ID: "a1"})
	f.Fail(providertest.OpTenants, dnsprovider.ErrUnauthorized)

	var built []string

	if _, err := provider.Open(context.Background(), tenantRegistry(f, &built), "tenanted", "bad-token"); !errors.Is(err, dnsprovider.ErrUnauthorized) {
		t.Errorf("Open error = %v, want ErrUnauthorized", err)
	}
}

func TestTheTenantIsListedOncePerToken(t *testing.T) {
	f := providertest.New()
	f.SetTenants(dnsprovider.Tenant{ID: "a1"})

	var built []string

	reg := tenantRegistry(f, &built)
	for range 3 {
		if _, err := provider.Open(context.Background(), reg, "tenanted", "listed-once-token"); err != nil {
			t.Fatalf("Open: %v", err)
		}
	}

	listed := 0

	for _, c := range f.Calls() {
		if c.Op == providertest.OpTenants {
			listed++
		}
	}

	if listed != 1 {
		t.Errorf("Tenants listed %d times, want once", listed)
	}
}

func TestAProviderWithoutTenantsIsBuiltAsIs(t *testing.T) {
	f := providertest.New()

	reg := dnsprovider.Registry{"fake": f.Factory()}
	if _, err := provider.Open(context.Background(), reg, "fake", "tok"); err != nil {
		t.Fatalf("Open: %v", err)
	}

	calls := f.Calls()
	if len(calls) != 1 || calls[0].Op != providertest.OpNew || calls[0].Fields["token"] != "tok" {
		t.Errorf("calls = %+v, want one build with the token", calls)
	}
}
