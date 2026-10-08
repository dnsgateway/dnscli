package provider_test

import (
	"reflect"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providers/cloudflare"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

func TestRecordConvertsBothWaysWithTheProxyExtra(t *testing.T) {
	cached := dns.Record{
		ProviderID: "r1", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 1,
		Extras: map[string]string{"proxied": "true"},
	}
	module := dnsprovider.Record{
		ID: "r1", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 1,
		Extras: map[string]string{cloudflare.ExtraProxied: "true"},
	}

	if got := provider.ModuleRecord(cached); !reflect.DeepEqual(got, module) {
		t.Errorf("ModuleRecord(%+v) = %+v, want %+v", cached, got, module)
	}

	if got := provider.CacheRecord(module); !reflect.DeepEqual(got, cached) {
		t.Errorf("CacheRecord(%+v) = %+v, want %+v", module, got, cached)
	}
}

func TestRecordConversionSharesNoExtras(t *testing.T) {
	cached := dns.Record{ProviderID: "r1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 300, Extras: map[string]string{"proxied": "false"}}

	module := provider.ModuleRecord(cached)
	module.Extras["proxied"] = "true"

	back := provider.CacheRecord(module)
	back.Extras["proxied"] = "maybe"

	if cached.Extras["proxied"] != "false" || module.Extras["proxied"] != "true" {
		t.Errorf("converted Records share their Extras: cached %v, module %v", cached.Extras, module.Extras)
	}
}

func TestZoneConvertsBothWays(t *testing.T) {
	cached := dns.Zone{ID: 7, AccountID: 3, Name: "example.com", ProviderID: "z1", Serial: 42, Nameservers: []string{"ada.ns.example", "bob.ns.example"}}

	module := provider.ModuleZone(cached)
	if want := (dnsprovider.Zone{ID: "z1", Name: "example.com", Nameservers: []string{"ada.ns.example", "bob.ns.example"}}); !reflect.DeepEqual(module, want) {
		t.Errorf("ModuleZone(%+v) = %+v, want %+v", cached, module, want)
	}

	listed := dnsprovider.Zone{ID: "z2", Name: "example.net", State: dnsprovider.HostingActive, Nameservers: []string{"ada.ns.example"}}
	if got, want := provider.CacheZone(listed), (dns.Zone{Name: "example.net", ProviderID: "z2", Nameservers: []string{"ada.ns.example"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("CacheZone(%+v) = %+v, want %+v", listed, got, want)
	}
}

func TestChangesetConvertsBothWays(t *testing.T) {
	zone := dns.Zone{ID: 7, Name: "example.com", ProviderID: "z1"}
	a := dns.Record{ProviderID: "r1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 300}
	b := dns.Record{Type: "A", Name: "example.com", Value: "192.0.2.2", TTL: 300}
	cs := dns.Changeset{Zone: zone, Changes: []dns.Change{
		{Kind: dns.ChangeAdd, After: b},
		{Kind: dns.ChangeModify, Before: a, After: b},
		{Kind: dns.ChangeDelete, Before: a},
	}}

	ma := dnsprovider.Record{ID: "r1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 300}
	mb := dnsprovider.Record{Type: "A", Name: "example.com", Value: "192.0.2.2", TTL: 300}
	want := dnsprovider.Changeset{Zone: dnsprovider.Zone{ID: "z1", Name: "example.com"}, Changes: []dnsprovider.Change{
		{Kind: dnsprovider.ChangeAdd, After: mb},
		{Kind: dnsprovider.ChangeModify, Before: ma, After: mb},
		{Kind: dnsprovider.ChangeDelete, Before: ma},
	}}

	got := provider.ModuleChangeset(cs)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ModuleChangeset = %+v, want %+v", got, want)
	}

	for i, c := range got.Changes {
		if back := provider.CacheChange(c); !reflect.DeepEqual(back, cs.Changes[i]) {
			t.Errorf("CacheChange(%+v) = %+v, want %+v", c, back, cs.Changes[i])
		}
	}
}
