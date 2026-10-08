//go:build fakeprovider

package main

import (
	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"
)

// A development build (go build -tags fakeprovider) offers an in-memory
// "fake" Provider with a few Zones, to try adding an Account without a
// real Provider. Its Zones and Records live only for the session.
func init() {
	f := providertest.New().
		AddZone(dnsprovider.Zone{ID: "fz1", Name: "example.com", State: dnsprovider.HostingActive},
			dnsprovider.Record{ID: "fr1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 300}).
		AddZone(dnsprovider.Zone{ID: "fz2", Name: "example.net", State: dnsprovider.HostingActive}).
		SetSerial("fz1", 2026100701)

	registry["fake"] = f.Factory()
}
