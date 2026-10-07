//go:build fakeprovider

package main

import (
	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
)

// A development build (go build -tags fakeprovider) offers an in-memory
// "fake" Provider with a few Zones, to try adding an Account without a
// real Provider. Its Zones and Records live only for the session.
func init() {
	f := fake.New().
		AddZone(dns.Zone{Name: "example.com", ProviderID: "fz1", Serial: 2026100701},
			dns.Record{ProviderID: "fr1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 300}).
		AddZone(dns.Zone{Name: "example.net", ProviderID: "fz2"})

	providers["fake"] = f.New
}
