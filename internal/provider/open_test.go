package provider_test

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

	// listing, when set, is sent to as the Tenants are listed, which then
	// wait for release.
	listing chan struct{}
	release chan struct{}
}

func newTenanted(tenants ...dnsprovider.Tenant) *tenanted {
	f := providertest.New()
	f.SetTenants(tenants...)

	return &tenanted{fake: f}
}

func (r *tenanted) registry() dnsprovider.Registry {
	inner := r.fake.Factory()
	fields := slices.DeleteFunc(slices.Clone(inner.Fields), func(fd dnsprovider.Field) bool { return fd.Tenant })

	return dnsprovider.Registry{"tenanted": {
		// Named other than Cloudflare's, so only the mark tells it.
		Fields: append(fields, dnsprovider.Field{Name: "scope", Label: "Account", Tenant: true}),
		New: func(m map[string]string, opts ...dnsprovider.Option) (dnsprovider.Provider, error) {
			r.mu.Lock()
			r.built = append(r.built, m["scope"])
			r.mu.Unlock()

			im := map[string]string{"token": m["token"]}
			if m["scope"] != "" {
				im[providertest.FieldTenant] = m["scope"]
			}

			p, err := inner.New(im, opts...)
			if err != nil {
				return nil, err
			}

			return scoped{Provider: p, scope: m["scope"], owned: strings.HasPrefix(m["token"], ownedPrefix), r: r}, nil
		},
	}}
}

// ownedPrefix marks a token owned by a Tenant, as Cloudflare's
// account-owned tokens are: it verifies only within its Tenant.
const ownedPrefix = "owned-"

// scoped is a Provider built by tenanted, which honours the Tenant it was
// built with, as Cloudflare does: unscoped, a Tenant-owned token is
// refused; scoped, the Account names its Tenant and expects nameservers
// of its own, ns.<Tenant>.
type scoped struct {
	dnsprovider.Provider
	scope string
	owned bool
	r     *tenanted
}

func (s scoped) Tenants(ctx context.Context) ([]dnsprovider.Tenant, error) {
	if s.r.listing != nil {
		s.r.listing <- struct{}{}
		<-s.r.release
	}

	return s.Provider.(dnsprovider.TenantLister).Tenants(ctx)
}

func (s scoped) Account(ctx context.Context) (dnsprovider.AccountInfo, error) {
	if s.scope == "" && s.owned {
		return dnsprovider.AccountInfo{}, dnsprovider.ErrUnauthorized
	}

	info, err := s.Provider.Account(ctx)
	if err == nil && s.scope != "" {
		info.Tenant = dnsprovider.Tenant{ID: s.scope}
		info.Nameservers = []string{"ns." + s.scope}
	}

	return info, err
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

func TestATokenReachingNoTenantsRunsUnscoped(t *testing.T) {
	r := newTenanted()

	open(t, provider.NewOpener(r.registry()), "tok")

	if b := r.builds(); !slices.Equal(b, []string{""}) {
		t.Errorf("built with Tenants %q, want one unscoped build", b)
	}
}

func TestATokenReachingSeveralTenantsIsOpenedOnlyOnceOneIsChosen(t *testing.T) {
	tenants := []dnsprovider.Tenant{{ID: "a1", Name: "Acme Ltd"}, {ID: "b2", Name: "Side project"}}
	r := newTenanted(tenants...)

	_, err := provider.NewOpener(r.registry()).Open(context.Background(), "tenanted", creds("tok"))

	var choice *provider.TenantChoiceError
	if !errors.As(err, &choice) {
		t.Fatalf("Open = %v, want a TenantChoiceError", err)
	}

	if !slices.Equal(choice.Tenants, tenants) {
		t.Errorf("Tenants to choose from = %+v, want %+v", choice.Tenants, tenants)
	}

	// Only the Tenants were asked for.
	for _, c := range r.fake.Calls() {
		if c.Op != providertest.OpNew && c.Op != providertest.OpTenants {
			t.Errorf("called %s before a Tenant was chosen", c.Op)
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
	for name, fail := range map[string]bool{"scoped": false, "unscoped as the token may not list them": true} {
		r := newTenanted(dnsprovider.Tenant{ID: tenantID})
		if fail {
			r.fake.Fail(providertest.OpTenants, dnsprovider.ErrUnauthorized)
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

func TestAFailedTenantListFailsTheOpenAndIsAskedAgain(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: "a1"}, dnsprovider.Tenant{ID: "b2"})
	r.fake.Fail(providertest.OpTenants, dnsprovider.ErrRateLimited)

	o := provider.NewOpener(r.registry())

	// Running unscoped could reach every Tenant's Zones: the Open fails.
	if _, err := o.Open(context.Background(), "tenanted", creds("tok")); !errors.Is(err, dnsprovider.ErrRateLimited) {
		t.Fatalf("Open = %v, want ErrRateLimited", err)
	}

	r.fake.Fail(providertest.OpTenants, nil)

	var choice *provider.TenantChoiceError
	if _, err := o.Open(context.Background(), "tenanted", creds("tok")); !errors.As(err, &choice) {
		t.Errorf("Open after the failure = %v, want the Tenants listed again", err)
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

func TestOpensWaitingOnAFailedTenantListGetItsError(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: "a1"}, dnsprovider.Tenant{ID: "b2"})
	r.fake.Fail(providertest.OpTenants, dnsprovider.ErrRateLimited)
	r.listing, r.release = make(chan struct{}, 8), make(chan struct{})

	o := provider.NewOpener(r.registry())
	errs := make(chan error, 4)

	open := func() {
		_, err := o.Open(context.Background(), "tenanted", creds("tok"))
		errs <- err
	}

	go open()
	<-r.listing

	// These wait for the listing in flight.
	for range 3 {
		go open()
	}

	time.Sleep(50 * time.Millisecond)
	close(r.release)

	for range 4 {
		if err := <-errs; !errors.Is(err, dnsprovider.ErrRateLimited) {
			t.Errorf("Open = %v, want the listing's ErrRateLimited", err)
		}
	}
}

func TestOpenScopedReturnsTheFieldsTheProviderIsBuiltFromAndBuildsOnce(t *testing.T) {
	for name, tc := range map[string]struct {
		tenants []dnsprovider.Tenant
		builds  []string
		scope   string
	}{
		"no Tenants": {builds: []string{""}},
		"one Tenant": {tenants: []dnsprovider.Tenant{{ID: tenantID}}, builds: []string{"", tenantID}, scope: tenantID},
	} {
		r := newTenanted(tc.tenants...)

		p, scoped, err := provider.NewOpener(r.registry()).OpenScoped(context.Background(), "tenanted", creds("tok"))
		if err != nil || p == nil {
			t.Fatalf("%s: OpenScoped = %v, %v", name, p, err)
		}

		if want := map[string]string{"token": "tok", "scope": tc.scope}; tc.scope == "" && !maps.Equal(scoped, creds("tok")) || tc.scope != "" && !maps.Equal(scoped, want) {
			t.Errorf("%s: scoped fields = %v", name, scoped)
		}

		if b := r.builds(); !slices.Equal(b, tc.builds) {
			t.Errorf("%s: built with Tenants %q, want %q", name, b, tc.builds)
		}
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

func TestVerifyRefusesCredentialsTheProviderRefuses(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID})
	r.fake.Fail(providertest.OpAccount, dnsprovider.ErrUnauthorized)

	_, _, err := provider.NewOpener(r.registry()).Verify(context.Background(), "tenanted", creds("bad"))
	if !errors.Is(err, dnsprovider.ErrUnauthorized) {
		t.Fatalf("Verify = %v, want ErrUnauthorized", err)
	}

	r.fake.Fail(providertest.OpAccount, nil)

	if _, _, err := provider.NewOpener(r.registry()).Verify(context.Background(), "tenanted", creds("good")); err != nil {
		t.Errorf("Verify of a good token = %v", err)
	}
}

func TestVerifyScopesATenantOwnedTokenToTheOnlyTenantItReaches(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID, Name: "Acme Ltd"})
	o := provider.NewOpener(r.registry())

	if _, _, err := o.Verify(context.Background(), "tenanted", creds(ownedPrefix+"tok")); err != nil {
		t.Fatalf("Verify of a Tenant-owned token = %v, want it verified within its Tenant", err)
	}

	if b := r.builds(); len(b) == 0 || b[len(b)-1] != tenantID {
		t.Errorf("built with Tenants %q, want the only one last", b)
	}

	// Opening the credentials again keeps the Tenant Verify found.
	open(t, o, ownedPrefix+"tok")

	if n := count(r.fake, providertest.OpTenants); n != 1 {
		t.Errorf("Tenants listed %d times, want once", n)
	}
}

func TestVerifyKeepsTheNameserversOfTheProviderOpenBuilds(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID})
	r.fake.SetAccount(dnsprovider.AccountInfo{Nameservers: []string{"ns.unscoped"}})

	o := provider.NewOpener(r.registry())
	ctx := context.Background()

	scoped, _, err := o.Verify(ctx, "tenanted", creds("tok"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// The credentials are saved as Verify returns them.
	p, err := o.Open(ctx, "tenanted", scoped)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if ns, err := o.Nameservers(ctx, p, "tenanted", scoped); err != nil || !slices.Equal(ns, []string{"ns." + tenantID}) {
		t.Errorf("Nameservers = %q, %v; want the scoped Account's", ns, err)
	}

	if n := count(r.fake, providertest.OpAccount); n != 1 {
		t.Errorf("Account asked %d times, want once, to verify", n)
	}
}

func TestVerifiedCredentialsKeepTheirAccountsNameservers(t *testing.T) {
	f := providertest.New()
	f.SetAccount(dnsprovider.AccountInfo{Nameservers: []string{"ns1.example.net"}})

	o := provider.NewOpener(dnsprovider.Registry{"fake": f.Factory()})
	ctx := context.Background()

	if _, _, err := o.Verify(ctx, "fake", creds("tok")); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	p, err := o.Open(ctx, "fake", creds("tok"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if ns, err := o.Nameservers(ctx, p, "fake", creds("tok")); err != nil || !slices.Equal(ns, []string{"ns1.example.net"}) {
		t.Fatalf("Nameservers = %q, %v; want the Account's", ns, err)
	}

	if n := count(f, providertest.OpAccount); n != 1 {
		t.Errorf("Account asked %d times, want once, to verify", n)
	}
}

func TestVerifyTellsFieldsItCannotBuildFromFromARefusal(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID})

	// The fake cannot be built without a token.
	_, _, err := provider.NewOpener(r.registry()).Verify(context.Background(), "tenanted", creds(""))

	var invalid *provider.InvalidCredentialsError
	if !errors.As(err, &invalid) {
		t.Fatalf("Verify = %v, want an InvalidCredentialsError", err)
	}

	if n := count(r.fake, providertest.OpAccount); n != 0 {
		t.Errorf("Account asked %d times, want none", n)
	}

	r.fake.Fail(providertest.OpAccount, dnsprovider.ErrUnauthorized)

	if _, _, err := provider.NewOpener(r.registry()).Verify(context.Background(), "tenanted", creds("bad")); errors.As(err, &invalid) {
		t.Errorf("Verify of refused credentials = %v, an InvalidCredentialsError", err)
	}
}

func TestVerifyReturnsTheCredentialsWithTheTenantTheyActWithin(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID, Name: "Acme Ltd"})
	fields := creds("tok")

	scoped, tenant, err := provider.NewOpener(r.registry()).Verify(context.Background(), "tenanted", fields)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if want := map[string]string{"token": "tok", "scope": tenantID}; !maps.Equal(scoped, want) {
		t.Errorf("verified credentials = %v, want %v", scoped, want)
	}

	if tenant != (dnsprovider.Tenant{ID: tenantID, Name: "Acme Ltd"}) {
		t.Errorf("Tenant = %+v, want Acme Ltd", tenant)
	}

	if len(fields) != 1 {
		t.Errorf("Verify changed its field map to %v", fields)
	}
}

func TestVerifyAsksForATenantBeforeAskingTheProvider(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: "a1"}, dnsprovider.Tenant{ID: "b2"})

	_, _, err := provider.NewOpener(r.registry()).Verify(context.Background(), "tenanted", creds("tok"))

	var choice *provider.TenantChoiceError
	if !errors.As(err, &choice) || len(choice.Tenants) != 2 {
		t.Fatalf("Verify = %v, want a TenantChoiceError with both Tenants", err)
	}

	if n := count(r.fake, providertest.OpAccount); n != 0 {
		t.Errorf("Account asked %d times, want none", n)
	}
}

func TestVerifyOfCredentialsWithoutTenantsReturnsThemAsTheyAre(t *testing.T) {
	f := providertest.New()

	scoped, tenant, err := provider.NewOpener(dnsprovider.Registry{"fake": f.Factory()}).Verify(context.Background(), "fake", creds("tok"))
	if err != nil || !maps.Equal(scoped, creds("tok")) || tenant != (dnsprovider.Tenant{}) {
		t.Errorf("Verify = %v, %+v, %v; want the credentials as they are, no Tenant", scoped, tenant, err)
	}
}

// everyTenant opens fields as OpenScoped does, then the Provider acting
// on every Tenant they reach.
func everyTenant(t *testing.T, o *provider.Opener, typ string, fields map[string]string) dnsprovider.Provider {
	t.Helper()

	_, scopedFields, err := o.OpenScoped(context.Background(), typ, fields)
	if err != nil {
		t.Fatalf("OpenScoped: %v", err)
	}

	every, err := o.EveryTenant(context.Background(), typ, scopedFields)
	if err != nil {
		t.Fatalf("EveryTenant: %v", err)
	}

	return every
}

func TestCredentialsReachingSeveralTenantsListZonesOfEveryOneUnscopedBuiltOnce(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID}, dnsprovider.Tenant{ID: "b2"})
	o := provider.NewOpener(r.registry())

	every := everyTenant(t, o, "tenanted", map[string]string{"token": "tok", "scope": "b2"})
	everyTenant(t, o, "tenanted", map[string]string{"token": "tok", "scope": "b2"})

	if s, ok := every.(scoped); !ok || s.scope != "" {
		t.Errorf("every-Tenant Provider = %#v, want one built unscoped", every)
	}

	// The unscoped build that listed the Tenants is the one kept.
	if b := r.builds(); !slices.Equal(b, []string{"b2", "", "b2"}) {
		t.Errorf("built with Tenants %q, want the stored one each time, and unscoped once", b)
	}

	if n := count(r.fake, providertest.OpTenants); n != 1 {
		t.Errorf("Tenants listed %d times, want once", n)
	}
}

func TestCredentialsReachingOneTenantListZonesWithTheProviderOpened(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID})

	if every := everyTenant(t, provider.NewOpener(r.registry()), "tenanted", creds("tok")); every != nil {
		t.Errorf("every-Tenant Provider = %#v, want none: the one opened lists every Zone", every)
	}
}

func TestAProviderWithoutTenantsListsZonesWithTheProviderOpened(t *testing.T) {
	f := providertest.New()
	reg := dnsprovider.Registry{"plain": f.Factory()}

	if every := everyTenant(t, provider.NewOpener(reg), "plain", creds("tok")); every != nil {
		t.Errorf("every-Tenant Provider = %#v, want none", every)
	}

	if n := count(f, providertest.OpTenants); n != 0 {
		t.Errorf("Tenants listed %d times, want none", n)
	}
}

func TestATenantListThatFailsLeavesWhetherThereAreSeveralUnknown(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID}, dnsprovider.Tenant{ID: "b2"})
	r.fake.Fail(providertest.OpTenants, errors.New("rate limited"))

	every, err := provider.NewOpener(r.registry()).EveryTenant(context.Background(), "tenanted", map[string]string{"token": "tok", "scope": "b2"})
	if every != nil || err == nil {
		t.Errorf("EveryTenant = %#v, %v; want no Provider and the listing's error", every, err)
	}
}

func TestATenantFieldStoredEmptySharesTheTenantList(t *testing.T) {
	r := newTenanted(dnsprovider.Tenant{ID: tenantID}, dnsprovider.Tenant{ID: "b2"})
	o := provider.NewOpener(r.registry())

	// Stored empty, the Tenants are listed to choose one.
	if _, _, err := o.OpenScoped(context.Background(), "tenanted", map[string]string{"token": "tok", "scope": ""}); err == nil {
		t.Fatal("OpenScoped of credentials reaching several Tenants, none chosen: no error")
	}

	everyTenant(t, o, "tenanted", map[string]string{"token": "tok", "scope": "b2"})

	if n := count(r.fake, providertest.OpTenants); n != 1 {
		t.Errorf("Tenants listed %d times, want once", n)
	}
}
