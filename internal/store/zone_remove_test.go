// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
)

func TestRemoveZoneDropsTheZoneWithItsRecordsAndLeavesTheRest(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	z := putZone(t, s, dns.Zone{Name: "example.com", ProviderID: "z1"})

	kept, _, err := s.PutZone(ctx, dns.Zone{AccountID: z.AccountID, Name: "example.org", ProviderID: "z2"})
	if err != nil {
		t.Fatal(err)
	}

	rec := []dns.Record{{ProviderID: "r1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 300}}
	if err := s.ReplaceRecords(ctx, z.ID, rec); err != nil {
		t.Fatal(err)
	}

	if err := s.RemoveZone(ctx, z); err != nil {
		t.Fatalf("RemoveZone: %v", err)
	}

	if got := onlyZone(t, s); got.ID != kept.ID {
		t.Errorf("cached = %+v, want example.org alone", got)
	}

	if records, err := s.Records(ctx, z.ID); err != nil || len(records) != 0 {
		t.Errorf("Records of the removed Zone = %+v, %v; want none", records, err)
	}
}

func TestRemoveZoneOfAZoneNoLongerCachedIsErrZoneNotCachedAndRemovesNothing(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))

	z := putZone(t, s, dns.Zone{Name: "example.com", ProviderID: "z1"})

	// Another zone took the name at the Provider meanwhile.
	if _, _, err := s.PutZone(ctx, dns.Zone{AccountID: z.AccountID, Name: "example.com", ProviderID: "z2"}); err != nil {
		t.Fatal(err)
	}

	if err := s.RemoveZone(ctx, z); !errors.Is(err, store.ErrZoneNotCached) {
		t.Fatalf("RemoveZone error = %v, want ErrZoneNotCached", err)
	}

	if got := onlyZone(t, s); got.ProviderID != "z2" {
		t.Fatalf("cached = %+v, want the other zone kept", got)
	}
}
