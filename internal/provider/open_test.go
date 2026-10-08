package provider_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
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
			// opened, and runs unscoped when it may not; the others are
			// first called with Account.
			o := provider.NewOpener(providers.Registry(), dnsprovider.WithBaseURL(base))

			fields, err := o.Credentials(tc.typ, []byte(tc.token))
			if err != nil {
				t.Fatalf("Credentials: %v", err)
			}

			p, err := o.Open(context.Background(), tc.typ, fields)
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
		o := provider.NewOpener(providers.Registry())

		fields, err := o.Credentials(tc.typ, []byte(tc.token))
		if err == nil {
			_, err = o.Open(context.Background(), tc.typ, fields)
		}

		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Open(%s, %q) error = %v, want one naming %s", tc.typ, tc.token, err, tc.want)
		}
	}
}

func TestOpenRefusesAnUnknownProviderType(t *testing.T) {
	if _, err := provider.NewOpener(providers.Registry()).Open(context.Background(), "route53", creds("tok")); err == nil {
		t.Error("Open(route53) succeeded, want an error")
	}
}

// tenantID is a Tenant the fake lists.
const tenantID = "0123456789abcdef0123456789abcdef"

// tenanted is a Registry whose "tenanted" Provider takes a Tenant in a
// field marked as one, as Cloudflare does, and builds the fake; built
// records the Tenant of each build, empty for none.
type tenanted struct {
	fake *providertest.Provider

	mu    sync.Mutex
	built []string
}

func newTenanted(tenants ...dnsprovider.Tenant) *tenanted {
	f := providertest.New()
	f.SetTenants(tenants...)

	return &tenanted{fake: f}
}

func (r *tenanted) registry() dnsprovider.Registry {
	inner := r.fake.Factory()

	return dnsprovider.Registry{"tenanted": {
		// Named other than Cloudflare's, so only the mark tells it.
		Fields: append(inner.Fields, dnsprovider.Field{Name: "scope", Label: "Account", Tenant: true}),
		New: func(m map[string]string, opts ...dnsprovider.Option) (dnsprovider.Provider, error) {
			r.mu.Lock()
			r.built = append(r.built, m["scope"])
			r.mu.Unlock()

			return inner.New(map[string]string{"token": m["token"]}, opts...)
		},
	}}
}

func (r *tenanted) builds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.built)
}

// count is how many calls of op the fake recorded.
func count(f *providertest.Provider, op providertest.Op) int {
	n := 0

	for _, c := range f.Calls() {
		if c.Op == op {
			n++
		}
	}

	return n
}

// creds is the field map of a Provider that takes one API token.
func creds(token string) map[string]string {
	return map[string]string{"token": token}
}

func open(t *testing.T, o *provider.Opener, token string) {
	t.Helper()

	if _, err := o.Open(context.Background(), "tenanted", creds(token)); err != nil {
		t.Fatalf("Open: %v", err)
	}
}

func TestATokenReachingOneTenantIsScopedToIt(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID, Name: "Acme Ltd"})

	open(t, provider.NewOpener(r.registry()), "tok")

	if b := r.builds(); len(b) == 0 || b[len(b)-1] != tenantID {
		t.Errorf("built with Tenants %q, want the only one last", b)
	}
}

func TestATokenReachingNoneOrSeveralTenantsRunsUnscoped(t *testing.T) {
	for name, tenants := range map[string][]dnsprovider.Tenant{
		"none":    {},
		"several": {{ID: "a1", Name: "Acme Ltd"}, {ID: "b2", Name: "Side project"}},
	} {
		r := newTenanted(tenants...)

		open(t, provider.NewOpener(r.registry()), "tok")

		if b := r.builds(); !slices.Equal(b, []string{""}) {
			t.Errorf("%s: built with Tenants %q, want one unscoped build", name, b)
		}
	}
}

func TestATokenThatMayNotListTenantsRunsUnscoped(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID})
	r.fake.Fail(providertest.OpTenants, dnsprovider.ErrUnauthorized)

	open(t, provider.NewOpener(r.registry()), "tok")

	if b := r.builds(); !slices.Equal(b, []string{""}) {
		t.Errorf("built with Tenants %q, want one unscoped build", b)
	}
}

func TestTheTenantOutcomeIsKeptPerTokenAndBuildsOnce(t *testing.T) {
	for name, fail := range map[string]bool{"scoped": false, "unscoped after a failure": true} {
		r := newTenanted(dnsprovider.Tenant{ID: tenantID})
		if fail {
			r.fake.Fail(providertest.OpTenants, errors.New("network down"))
		}

		o := provider.NewOpener(r.registry())

		open(t, o, "tok")
		first := len(r.builds())

		for range 3 {
			open(t, o, "tok")
		}

		if n := count(r.fake, providertest.OpTenants); n != 1 {
			t.Errorf("%s: Tenants listed %d times, want once", name, n)
		}

		if n := len(r.builds()) - first; n != 3 {
			t.Errorf("%s: %d builds for 3 Opens with the outcome kept, want 3", name, n)
		}

		open(t, o, "other")

		if n := count(r.fake, providertest.OpTenants); n != 2 {
			t.Errorf("%s: Tenants listed %d times after another token, want twice", name, n)
		}
	}
}

func TestConcurrentOpensListTheTenantsOnce(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID})
	o := provider.NewOpener(r.registry())

	var wg sync.WaitGroup

	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if _, err := o.Open(context.Background(), "tenanted", creds("tok")); err != nil {
				t.Errorf("Open: %v", err)
			}
		}()
	}

	wg.Wait()

	if n := count(r.fake, providertest.OpTenants); n != 1 {
		t.Errorf("Tenants listed %d times, want once", n)
	}
}

func TestOpenersKeepNothingInCommon(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID})

	open(t, provider.NewOpener(r.registry()), "tok")
	open(t, provider.NewOpener(r.registry()), "tok")

	if n := count(r.fake, providertest.OpTenants); n != 2 {
		t.Errorf("Tenants listed %d times by two Openers, want twice", n)
	}
}

func TestAProviderWithoutTenantsIsBuiltAsIs(t *testing.T) {
	f := providertest.New()

	reg := dnsprovider.Registry{"fake": f.Factory()}
	if _, err := provider.NewOpener(reg).Open(context.Background(), "fake", creds("tok")); err != nil {
		t.Fatalf("Open: %v", err)
	}

	calls := f.Calls()
	if len(calls) != 1 || calls[0].Op != providertest.OpNew || calls[0].Fields["token"] != "tok" {
		t.Errorf("calls = %+v, want one build with the token", calls)
	}
}

func TestTheAccountNameserversAreAskedOncePerToken(t *testing.T) {
	f := providertest.New()
	f.SetAccount(dnsprovider.AccountInfo{Nameservers: []string{"ns1.example.net", "ns2.example.net"}})

	o := provider.NewOpener(dnsprovider.Registry{"fake": f.Factory()})
	ctx := context.Background()

	p, err := o.Open(ctx, "fake", creds("tok"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	for range 3 {
		ns, err := o.Nameservers(ctx, p, "fake", creds("tok"))
		if err != nil || !slices.Equal(ns, []string{"ns1.example.net", "ns2.example.net"}) {
			t.Fatalf("Nameservers = %q, %v; want the Account's", ns, err)
		}
	}

	if n := count(f, providertest.OpAccount); n != 1 {
		t.Errorf("Account asked %d times, want once", n)
	}
}

func TestAFailedNameserverReadIsAskedAgain(t *testing.T) {
	f := providertest.New()
	f.SetAccount(dnsprovider.AccountInfo{Nameservers: []string{"ns1.example.net"}})
	f.Fail(providertest.OpAccount, dnsprovider.ErrRateLimited)

	o := provider.NewOpener(dnsprovider.Registry{"fake": f.Factory()})
	ctx := context.Background()

	p, err := o.Open(ctx, "fake", creds("tok"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, err := o.Nameservers(ctx, p, "fake", creds("tok")); !errors.Is(err, dnsprovider.ErrRateLimited) {
		t.Fatalf("Nameservers: err = %v, want ErrRateLimited", err)
	}

	f.Fail(providertest.OpAccount, nil)

	if ns, err := o.Nameservers(ctx, p, "fake", creds("tok")); err != nil || !slices.Equal(ns, []string{"ns1.example.net"}) {
		t.Errorf("Nameservers after the failure = %q, %v; want the Account's", ns, err)
	}
}

func TestAStoredTenantScopesTheProviderWithoutListingTenants(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID}, dnsprovider.Tenant{ID: "b2"})

	if _, err := provider.NewOpener(r.registry()).Open(context.Background(), "tenanted", map[string]string{"token": "tok", "scope": "b2"}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if b := r.builds(); !slices.Equal(b, []string{"b2"}) {
		t.Errorf("built with Tenants %q, want the stored one only", b)
	}

	if n := count(r.fake, providertest.OpTenants); n != 0 {
		t.Errorf("Tenants listed %d times, want none", n)
	}
}

func TestOpenDoesNotChangeTheFieldMapItIsGiven(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID})
	fields := creds("tok")

	open := func() {
		if _, err := provider.NewOpener(r.registry()).Open(context.Background(), "tenanted", fields); err != nil {
			t.Fatalf("Open: %v", err)
		}
	}

	open()

	if len(fields) != 1 {
		t.Errorf("Open changed its field map to %v", fields)
	}
}

func TestVerifyAsksTheAccountWithTheFieldsAsTyped(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID})
	r.fake.Fail(providertest.OpAccount, dnsprovider.ErrUnauthorized)

	err := provider.NewOpener(r.registry()).Verify(context.Background(), "tenanted", creds("bad"))
	if !errors.Is(err, dnsprovider.ErrUnauthorized) {
		t.Fatalf("Verify = %v, want ErrUnauthorized", err)
	}

	if b := r.builds(); !slices.Equal(b, []string{""}) {
		t.Errorf("built with Tenants %q, want one build as typed", b)
	}

	if n := count(r.fake, providertest.OpTenants); n != 0 {
		t.Errorf("Tenants listed %d times, want none", n)
	}

	r.fake.Fail(providertest.OpAccount, nil)

	if err := provider.NewOpener(r.registry()).Verify(context.Background(), "tenanted", creds("good")); err != nil {
		t.Errorf("Verify of a good token = %v", err)
	}
}
