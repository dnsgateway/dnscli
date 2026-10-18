// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package store_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

func TestSaveApplyRefusesAChangeWithoutAProviderRecordID(t *testing.T) {
	cached := []dns.Record{
		{ProviderID: "r1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 300},
		{ProviderID: "", Type: "TXT", Name: "example.com", Value: "listed without an ID", TTL: 300},
	}

	cases := map[string]dns.Change{
		"an addition without one": {Kind: dns.ChangeAdd,
			After: dns.Record{Type: "TXT", Name: "example.com", Value: "new", TTL: 300}},
		"a modification of a Record without one": {Kind: dns.ChangeModify, Before: cached[1],
			After: dns.Record{Type: "TXT", Name: "example.com", Value: "changed", TTL: 300}},
		"a modification stored without one": {Kind: dns.ChangeModify, Before: cached[0],
			After: dns.Record{Type: "A", Name: "example.com", Value: "192.0.2.2", TTL: 300}},
		"a deletion of a Record without one": {Kind: dns.ChangeDelete, Before: cached[1]},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

			a, err := s.AddAccount(ctx, dns.Account{Provider: "cloudflare", Name: "work"})
			if err != nil {
				t.Fatalf("add account: %v", err)
			}

			z, _, err := s.PutZone(ctx, dns.Zone{AccountID: a.ID, Name: "example.com", ProviderID: "z1"})
			if err != nil {
				t.Fatalf("put zone: %v", err)
			}

			if err := s.ReplaceRecords(ctx, z.ID, cached); err != nil {
				t.Fatalf("cache records: %v", err)
			}

			ok := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{ProviderID: "r9", Type: "A", Name: "www.example.com", Value: "192.0.2.9", TTL: 300}}

			if err := s.SaveApply(ctx, z, []dns.Change{ok, c}, 0); err == nil {
				t.Fatal("SaveApply took a change without a Provider record ID")
			}

			got, err := s.Records(ctx, z.ID)
			if err != nil {
				t.Fatalf("records: %v", err)
			}

			if !reflect.DeepEqual(got, cached) {
				t.Fatalf("cached Records = %+v, want them unchanged %+v", got, cached)
			}
		})
	}
}
