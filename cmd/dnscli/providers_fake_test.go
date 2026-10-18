// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

//go:build fakeprovider

package main

import (
	"context"
	"slices"
	"testing"

	"github.com/dnsgateway/dnsprovider"
)

// The fake build's parent zone serves the DS once it is published at the
// fake Account, so publishing it can be tried through to DNSSEC On.
func TestTheFakeBuildServesTheDSPublishedAtTheFakeAccount(t *testing.T) {
	ctx := context.Background()
	r := dnsResolver

	if ns, err := r.NS(ctx, "example.net"); err != nil || !slices.Equal(ns, fakeNameservers) {
		t.Fatalf("NS = %v, %v; want the fake's nameservers", ns, err)
	}

	keys, err := r.DNSKEYAt(ctx, "example.net", fakeNameservers)
	if err != nil || len(keys) != 1 || keys[0].DNSKEY != fakeKey.DNSKEY {
		t.Fatalf("DNSKEYAt = %+v, %v; want the fake's key", keys, err)
	}

	if ds, _, err := r.DSAt(ctx, "example.net"); err != nil || len(ds) != 0 {
		t.Fatalf("DSAt before publishing = %v, %v; want none", ds, err)
	}

	p, err := registry["fake"].New(map[string]string{"token": "t"})
	if err != nil {
		t.Fatal(err)
	}

	z := dnsprovider.Zone{ID: "fz2", Name: "example.net"}
	if err := p.SetDelegationSigners(ctx, z, []dnsprovider.SigningKey{fakeKey}); err != nil {
		t.Fatal(err)
	}

	if ds, _, err := r.DSAt(ctx, "example.net"); err != nil || !slices.Equal(ds, fakeKey.DS) {
		t.Fatalf("DSAt once published = %v, %v; want %v", ds, err, fakeKey.DS)
	}
}
