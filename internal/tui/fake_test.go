package tui_test

import (
	"maps"
	"slices"
	"sync"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providers"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// fakeProvider is the module's providertest fake, scripted with dnscli's
// cache types. It is a Batcher, so dnsprovider.Apply hands it each
// Changeset whole, recorded as providertest.OpApply.
type fakeProvider struct {
	*providertest.Provider
}

func newFake() *fakeProvider {
	f := &fakeProvider{providertest.New()}
	f.SetBatcher(true)

	return f
}

// registry registers the fake as "cloudflare".
func (f *fakeProvider) registry() dnsprovider.Registry {
	return dnsprovider.Registry{"cloudflare": f.Factory()}
}

// AddZone scripts an Active Zone, identified by its ProviderID, with its
// serial and Records.
func (f *fakeProvider) AddZone(z dns.Zone, records ...dns.Record) *fakeProvider {
	mz := provider.ModuleZone(z)
	mz.State = dnsprovider.HostingActive

	f.Provider.AddZone(mz, moduleRecords(records)...)

	if z.Serial != 0 {
		f.SetSerial(z.ProviderID, z.Serial)
	}

	return f
}

// SetRecords replaces a Zone's Records, as if changed outside dnscli.
func (f *fakeProvider) SetRecords(zoneProviderID string, records ...dns.Record) {
	f.Provider.SetRecords(zoneProviderID, moduleRecords(records)...)
}

// SetNameservers scripts the nameservers the Account expects.
func (f *fakeProvider) SetNameservers(ns ...string) {
	f.SetAccount(dnsprovider.AccountInfo{Nameservers: ns})
}

// FailChanges makes each write fail a change for which fn returns an
// error, while applying the rest.
func (f *fakeProvider) FailChanges(fn func(dns.Change) error) {
	if fn == nil {
		f.Provider.FailChanges(nil)

		return
	}

	f.Provider.FailChanges(func(c dnsprovider.Change) error { return fn(provider.CacheChange(c)) })
}

// tokens are the API tokens the fake was built with, in order.
func (f *fakeProvider) tokens() []string {
	var out []string

	for _, c := range f.Calls() {
		if c.Op == providertest.OpNew {
			out = append(out, c.Fields["token"])
		}
	}

	return out
}

func moduleRecords(records []dns.Record) []dnsprovider.Record {
	out := make([]dnsprovider.Record, len(records))
	for i, r := range records {
		out[i] = provider.ModuleRecord(r)
	}

	return out
}

// applyCalls returns the Changesets the fake Provider was asked to apply,
// as dnscli's cache types.
func applyCalls(f *fakeProvider) []dns.Changeset {
	var out []dns.Changeset

	for _, c := range f.Calls() {
		if c.Op != providertest.OpApply {
			continue
		}

		cs := dns.Changeset{Zone: dns.Zone{Name: c.Changeset.Zone.Name, ProviderID: c.Changeset.Zone.ID}}
		for _, ch := range c.Changeset.Changes {
			cs.Changes = append(cs.Changes, provider.CacheChange(ch))
		}

		out = append(out, cs)
	}

	return out
}

// wrapped registers the fake as "cloudflare", each Provider it builds
// wrapped by wrap.
func wrapped(f *fakeProvider, wrap func(dnsprovider.Provider) dnsprovider.Provider) dnsprovider.Registry {
	inner := f.Factory()

	return dnsprovider.Registry{"cloudflare": {
		Fields: inner.Fields,
		New: func(m map[string]string, opts ...dnsprovider.Option) (dnsprovider.Provider, error) {
			p, err := inner.New(m, opts...)
			if err != nil {
				return nil, err
			}

			return wrap(p), nil
		},
	}}
}

// tenantBuilds registers the fake as "cloudflare" taking a Tenant in a
// field marked as one, as Cloudflare does, and keeps the Tenant each
// Provider was built with.
type tenantBuilds struct {
	mu      sync.Mutex
	tenants []string // empty for an unscoped build
}

// tenantRegistry is the Registry tb records the builds of.
func (f *fakeProvider) tenantRegistry(tb *tenantBuilds) dnsprovider.Registry {
	inner := f.Factory()

	return dnsprovider.Registry{"cloudflare": {
		Fields: append(slices.Clone(inner.Fields), dnsprovider.Field{Name: "account", Label: "Account", Tenant: true}),
		New: func(m map[string]string, opts ...dnsprovider.Option) (dnsprovider.Provider, error) {
			tb.mu.Lock()
			tb.tenants = append(tb.tenants, m["account"])
			tb.mu.Unlock()

			return inner.New(map[string]string{"token": m["token"]}, opts...)
		},
	}}
}

func (tb *tenantBuilds) built() []string {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	return slices.Clone(tb.tenants)
}

// count is how many calls of op the fake recorded.
func (f *fakeProvider) count(op providertest.Op) int {
	n := 0

	for _, c := range f.Calls() {
		if c.Op == op {
			n++
		}
	}

	return n
}

// fieldBuilds keeps the field map of each Provider built through a
// realFieldsRegistry.
type fieldBuilds struct {
	mu    sync.Mutex
	built []map[string]string
}

// realFieldsRegistry registers the fake under each of the module's
// Provider types, taking that Provider's Fields, so the Account forms show
// them; fb keeps the field map of each build.
func (f *fakeProvider) realFieldsRegistry(fb *fieldBuilds) dnsprovider.Registry {
	inner := f.Factory()
	out := dnsprovider.Registry{}

	for typ, real := range providers.Registry() {
		out[typ] = dnsprovider.Factory{
			Fields: real.Fields,
			New: func(m map[string]string, opts ...dnsprovider.Option) (dnsprovider.Provider, error) {
				fb.mu.Lock()
				fb.built = append(fb.built, maps.Clone(m))
				fb.mu.Unlock()

				return inner.New(map[string]string{"token": typ}, opts...)
			},
		}
	}

	return out
}

// last is the field map of the latest build; nil before any.
func (fb *fieldBuilds) last() map[string]string {
	fb.mu.Lock()
	defer fb.mu.Unlock()

	if len(fb.built) == 0 {
		return nil
	}

	return maps.Clone(fb.built[len(fb.built)-1])
}
