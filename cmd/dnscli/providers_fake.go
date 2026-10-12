//go:build fakeprovider

package main

import (
	"context"
	"errors"
	"time"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// fakeNameservers are the nameservers the fake's Zones are delegated to.
var fakeNameservers = []string{"ns1.fake.invalid", "ns2.fake.invalid"}

// fakeKey is example.net's key-signing key, the ECDSA P-256 key of
// RFC 6605, section 6.1, with its SHA-256 DS.
var fakeKey = dnsprovider.SigningKey{
	DNSKEY: "257 3 13 GojIhhXUN/u4v54ZQqGSnyhWJwaubCvTmeexv7bR6edbkrSqQpF64cYbcB7wNcP+e+MAnLr+Wi9xMWyQLc8NAA==",
	KeyTag: 55648,
	DS:     []string{"55648 13 2 B4C8C1FE2E7477127B27115656AD6256F424625BF5C1E2770CE6D6E37DF61D17"},
}

// A development build (go build -tags fakeprovider) offers an in-memory
// "fake" Provider with a few Zones, to try adding an Account without a
// real Provider. Its Zones and Records live only for the session.
//
// It also registers both its Zones, so publishing the DS at the registrar
// can be tried: example.net is signed, and once its wait has passed, its
// DS can be published at the fake Account. The build's resolver answers
// for the fake's Zones from the fake itself: the parent serves the DS the
// fake's registry holds.
func init() {
	f := providertest.New().
		AddZone(dnsprovider.Zone{ID: "fz1", Name: "example.com", State: dnsprovider.HostingActive, Nameservers: fakeNameservers},
			dnsprovider.Record{ID: "fr1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 300}).
		AddZone(dnsprovider.Zone{ID: "fz2", Name: "example.net", State: dnsprovider.HostingActive, Nameservers: fakeNameservers},
			dnsprovider.Record{ID: "fr2", Type: "A", Name: "example.net", Value: "192.0.2.2", TTL: 60}).
		SetSerial("fz1", 2026100701)
	f.SetAccount(dnsprovider.AccountInfo{Nameservers: fakeNameservers})
	f.SetZoneStatus("fz2", dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On})
	f.SetSigningKeys("fz2", fakeKey)
	f.SetZoneUpdater(true)
	f.SetRegistration("fz1")
	f.SetRegistration("fz2")

	registry["fake"] = f.Factory()
	dnsResolver = &fakeResolver{fake: f, zones: map[string]dnsprovider.Zone{
		"example.com": {ID: "fz1", Name: "example.com"},
		"example.net": {ID: "fz2", Name: "example.net"},
	}, next: dnsResolver}
}

// errFakeSOA is the fake's Zones' serial, which the fake build's resolver
// does not serve.
var errFakeSOA = errors.New("the fake build serves no SOA")

// fakeResolver answers for the fake's Zones from the fake: their
// Delegation, their keys and the DS its registry holds. It asks next for
// any other name.
type fakeResolver struct {
	fake  *providertest.Provider
	zones map[string]dnsprovider.Zone
	next  tui.Resolver
}

// provider builds a Provider of the fake, to read it as its registry and
// its nameservers would serve it.
func (r *fakeResolver) provider() (dnsprovider.Provider, error) {
	return r.fake.Factory().New(map[string]string{"token": "resolver"})
}

func (r *fakeResolver) SOA(ctx context.Context, zone string) (uint32, error) {
	if _, ok := r.zones[zone]; ok {
		return 0, errFakeSOA
	}

	return r.next.SOA(ctx, zone)
}

func (r *fakeResolver) NS(ctx context.Context, zone string) ([]string, error) {
	if _, ok := r.zones[zone]; ok {
		return fakeNameservers, nil
	}

	return r.next.NS(ctx, zone)
}

func (r *fakeResolver) SOAAt(ctx context.Context, zone string, servers []string) (uint32, error) {
	if _, ok := r.zones[zone]; ok {
		return 0, errFakeSOA
	}

	return r.next.SOAAt(ctx, zone, servers)
}

// DSAt serves the DS the fake's registry holds for a fake Zone, with an
// hour-long TTL.
func (r *fakeResolver) DSAt(ctx context.Context, zone string) ([]string, time.Duration, error) {
	z, ok := r.zones[zone]
	if !ok {
		return r.next.DSAt(ctx, zone)
	}

	p, err := r.provider()
	if err != nil {
		return nil, 0, err
	}

	keys, err := p.ListDelegationSigners(ctx, z)
	if err != nil {
		return nil, 0, err
	}

	var ds []string
	for _, k := range keys {
		ds = append(ds, k.DS...)
	}

	return ds, time.Hour, nil
}

// DNSKEYAt serves the keys the fake signs a fake Zone with.
func (r *fakeResolver) DNSKEYAt(ctx context.Context, zone string, servers []string) ([]dns.SigningKey, error) {
	z, ok := r.zones[zone]
	if !ok {
		return r.next.DNSKEYAt(ctx, zone, servers)
	}

	p, err := r.provider()
	if err != nil {
		return nil, err
	}

	details, err := p.ZoneDetails(ctx, z, dnsprovider.ZoneDetailRequest{SigningKeys: true})

	return provider.SigningKeys(details.SigningKeys), err
}
