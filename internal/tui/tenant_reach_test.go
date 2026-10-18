// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// otherTenants is a Provider the fake built unscoped, which lists, as
// Cloudflare's does, the Zones of every Tenant the token reaches: the
// fake's, which are the Account's own Tenant's, and zones of another.
type otherTenants struct {
	dnsprovider.Provider
	zones []dnsprovider.Zone
}

func (o otherTenants) ListZones(ctx context.Context, f dnsprovider.ZoneFilter) (dnsprovider.ZoneSet, error) {
	zones, err := o.Provider.ListZones(ctx, f)
	if err != nil {
		return dnsprovider.ZoneSet{}, err
	}

	for _, z := range o.zones {
		if strings.Contains(z.Name, f.Contains) {
			zones.Zones = append(zones.Zones, z)
		}
	}

	return zones, nil
}

// otherTenantsRegistry is the fake's Tenant-taking Registry whose
// Providers built unscoped list others too.
func otherTenantsRegistry(f *fakeProvider, others ...dnsprovider.Zone) dnsprovider.Registry {
	reg := f.tenantRegistry(&tenantBuilds{})
	factory := reg["cloudflare"]
	inner := factory.New

	factory.New = func(m map[string]string, opts ...dnsprovider.Option) (dnsprovider.Provider, error) {
		p, err := inner(m, opts...)
		if err != nil || m["account"] != "" {
			return p, err
		}

		return otherTenants{Provider: p, zones: others}, nil
	}

	reg["cloudflare"] = factory

	return reg
}

// accountZones are the cached Zones of an Account, by name.
func accountZones(t *testing.T, s *store.Store, id int64) map[string]dns.Zone {
	t.Helper()

	zones, err := s.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	out := map[string]dns.Zone{}

	for _, z := range zones {
		if z.AccountID == id {
			out[z.Name] = z
		}
	}

	return out
}

func TestRelistingKeepsTheAccountsOwnTenantsZoneOverAnotherTenantsOfThatName(t *testing.T) {
	f := newFake().AddZone(dns.Zone{Name: "shared.example", ProviderID: "own1"})
	f.SetHostingState("own1", dnsprovider.HostingPending)
	f.SetTenants(twoTenants...)

	s, _ := enrollFirstAccount(t)
	old := addOldAccount(t, s, "cloudflare", "old", `{"token":"tok","account":"b2"}`)

	// Another Tenant holds shared.example Active, and other.example.
	reg := otherTenantsRegistry(f,
		dnsprovider.Zone{ID: "oth1", Name: "shared.example", State: dnsprovider.HostingActive},
		dnsprovider.Zone{ID: "oth2", Name: "other.example", State: dnsprovider.HostingActive})
	h := newHarnessWith(t, s, tui.Deps{Providers: reg, UnsealMethod: fastPassphrase()})

	h.press("shift+tab", "L")
	h.unsealWith(passphrase)
	h.assertShows("Re-listed cloudflare.old")

	zones := accountZones(t, s, old.ID)
	if z := zones["shared.example"]; z.ProviderID != "own1" {
		t.Errorf("shared.example cached as %+v, want the Account's own Tenant's own1, though Pending", z)
	}

	if z := zones["other.example"]; z.ProviderID != "oth2" {
		t.Errorf("other.example cached as %+v, want the other Tenant's oth2 listed too", z)
	}
}

func TestRelistingFailsWhenTheTenantsCannotBeListedLeavingTheCache(t *testing.T) {
	f := newFake().AddZone(dns.Zone{Name: "own.example", ProviderID: "own1"})
	f.SetTenants(twoTenants...)
	f.Fail(providertest.OpListTenants, &dnsprovider.APIError{Provider: "cloudflare", StatusCode: 503, Messages: []string{"unavailable"}})

	s, _ := enrollFirstAccount(t)
	old := addOldAccount(t, s, "cloudflare", "old", `{"token":"tok","account":"b2"}`)
	mustZone(t, s, dns.Zone{AccountID: old.ID, Name: "other.example", ProviderID: "oth2"})

	h := newHarnessWith(t, s, tui.Deps{Providers: otherTenantsRegistry(f), UnsealMethod: fastPassphrase()})

	h.press("shift+tab", "L")
	h.unsealWith(passphrase)
	h.assertShows("listing the Tenants")

	// Its own Tenant's Zones alone would have dropped other.example.
	zones := accountZones(t, s, old.ID)
	if _, ok := zones["other.example"]; !ok || len(zones) != 1 {
		t.Errorf("cached Zones = %v, want other.example kept and nothing added", zones)
	}
}

func TestAZoneOfAnotherTenantIsNotOffboarded(t *testing.T) {
	f := newFake()
	f.SetTenants(twoTenants...)
	f.SetZoneDeleter(true)

	s, _ := enrollFirstAccount(t)
	old := addOldAccount(t, s, "cloudflare", "old", `{"token":"tok","account":"b2"}`)
	mustZone(t, s, dns.Zone{AccountID: old.ID, Name: "other.example", ProviderID: "oth2",
		State: dnsprovider.HostingPending, Nameservers: workTestNS})

	reg := otherTenantsRegistry(f, dnsprovider.Zone{ID: "oth2", Name: "other.example", State: dnsprovider.HostingPending, Nameservers: workTestNS})
	r := &fakeResolver{ns: map[string][]string{"other.example": registrarNS}}
	h := newHarnessWith(t, s, tui.Deps{Providers: reg, UnsealMethod: fastPassphrase(), Resolver: r})

	// Show cloudflare.old's Zones, and Offboard other.example.
	h.press("shift+tab", "space", "tab", "d")
	h.unsealWith(passphrase)
	h.assertShows("Offboard other.example from cloudflare.old?")

	h.press("enter")
	h.assertShows("other.example is of another Tenant")

	if calls := deleteZoneCalls(f); len(calls) != 0 {
		t.Fatalf("DeleteZone calls = %q, want none", calls)
	}

	if !zoneCached(t, s, "other.example") {
		t.Fatal("other.example left the cache")
	}
}
