package tui_test

import (
	"slices"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/tui"
)

// acmeTenant is a Cloudflare account ID.
const acmeTenant = "0123456789abcdef0123456789abcdef"

// discoverRefreshAndApply adds cloudflare.work through tb's Registry,
// Refreshes work.example and applies a Changeset to it.
func discoverRefreshAndApply(t *testing.T, f *fakeProvider, tb *tenantBuilds) *harness {
	t.Helper()

	h, _, _ := newRefreshHarnessWith(t, f, tui.Deps{Providers: f.tenantRegistry(tb)})
	h.press("enter", "tab")
	h.assertShows("validated this session")
	h.buildChangeset()
	h.press("p", "enter")
	h.assertShows("Applied 3 changes to work.example.")

	return h
}

func TestAnAccountWhoseTokenReachesOneTenantActsWithinIt(t *testing.T) {
	f := refreshZones()
	f.SetTenants(dnsprovider.Tenant{ID: acmeTenant, Name: "Acme Ltd"})

	tb := &tenantBuilds{}
	discoverRefreshAndApply(t, f, tb)

	// The first build verifies the token as typed and the second lists
	// the Tenants; every later one is scoped.
	built := tb.built()
	if len(built) < 5 || built[0] != "" || built[1] != "" || slices.ContainsFunc(built[2:], func(id string) bool { return id != acmeTenant }) {
		t.Errorf("built with Tenants %q, want the first two unscoped and every later one scoped to %s", built, acmeTenant)
	}

	if n := f.count(providertest.OpTenants); n != 1 {
		t.Errorf("Tenants listed %d times, want once", n)
	}
}

func TestAnAccountWhoseTokenReachesSeveralTenantsOrMayNotListThemRunsUnscoped(t *testing.T) {
	for name, script := range map[string]func(f *fakeProvider){
		"several": func(f *fakeProvider) {
			f.SetTenants(dnsprovider.Tenant{ID: acmeTenant, Name: "Acme Ltd"}, dnsprovider.Tenant{ID: "b2", Name: "Side project"})
		},
		"may not list": func(f *fakeProvider) {
			f.SetTenants(dnsprovider.Tenant{ID: acmeTenant})
			f.Fail(providertest.OpTenants, dnsprovider.ErrUnauthorized)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := refreshZones()
			script(f)

			tb := &tenantBuilds{}
			discoverRefreshAndApply(t, f, tb)

			if built := tb.built(); slices.ContainsFunc(built, func(id string) bool { return id != "" }) {
				t.Errorf("built with Tenants %q, want every build unscoped", built)
			}

			if n := f.count(providertest.OpTenants); n != 1 {
				t.Errorf("Tenants listed %d times, want once", n)
			}
		})
	}
}
