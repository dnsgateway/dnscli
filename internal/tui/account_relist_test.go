package tui_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
)

func TestRelistingAddsNewZonesAndDropsGoneOnesKeepingTheCacheOfTheRest(t *testing.T) {
	ctx := context.Background()
	s, _ := enrollFirstAccount(t)
	personalZone := withPersonalAccount(t, s)

	// work.example has been Refreshed before; work.test is gone at the Provider.
	refreshed := testNow.Add(-time.Hour)
	workExample := zoneNamed(t, s, "work.example")
	workExample.Serial, workExample.RefreshedAt = 2026100703, refreshed
	mustZone(t, s, workExample)

	if err := s.ReplaceRecords(ctx, workExample.ID, []dns.Record{
		{ProviderID: "rw1", Type: "A", Name: "work.example", Value: "192.0.2.80", TTL: 300},
	}); err != nil {
		t.Fatal(err)
	}

	workTest := zoneNamed(t, s, "work.test")
	if err := s.ReplaceRecords(ctx, workTest.ID, []dns.Record{
		{ProviderID: "rw2", Type: "A", Name: "work.test", Value: "192.0.2.81", TTL: 300},
	}); err != nil {
		t.Fatal(err)
	}

	f := fake.New().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.new", ProviderID: "zw3"})
	h := newAccountHarness(t, s, f)

	h.press("tab", "j", "L")

	// Re-listing needs the API token: the sealed session Unseals first.
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("the Provider was called while sealed: %+v", calls)
	}

	h.unsealWith(passphrase)

	h.assertShows("Re-listed cloudflare.work: 1 Zone added, 1 dropped.", "work.example", "work.new", "personal.example")
	h.assertHides("work.test")

	if names := zoneNames(t, s); !slices.Equal(names, []string{"personal.example", "work.example", "work.new"}) {
		t.Fatalf("cached Zones = %v", names)
	}

	kept := zoneNamed(t, s, "work.example")
	if kept.ID != workExample.ID || kept.Serial != 2026100703 || !kept.RefreshedAt.Equal(refreshed) {
		t.Fatalf("work.example after re-listing = %+v, want ID %d, serial 2026100703, refreshed %v", kept, workExample.ID, refreshed)
	}

	if records, err := s.Records(ctx, workExample.ID); err != nil || len(records) != 1 || records[0].Value != "192.0.2.80" {
		t.Fatalf("Records of a kept Zone = %+v, %v", records, err)
	}

	if records, err := s.Records(ctx, workTest.ID); err != nil || len(records) != 0 {
		t.Fatalf("Records of a dropped Zone = %+v, %v", records, err)
	}

	if records, err := s.Records(ctx, personalZone.ID); err != nil || len(records) != 1 {
		t.Fatalf("Records of another Account = %+v, %v", records, err)
	}

	calls := f.Calls()
	if len(calls) != 2 || calls[0].Op != fake.OpNew || calls[0].Account.Label() != "cloudflare.work" || calls[0].Token != token || calls[1].Op != fake.OpZones {
		t.Fatalf("provider calls = %+v", calls)
	}
}

func TestAFailedRelistingLeavesTheCacheAndShowsTheError(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	f := fake.New().AddZone(dns.Zone{Name: "work.new", ProviderID: "zw3"})
	f.Fail(fake.OpZones, &provider.APIError{Provider: "cloudflare", StatusCode: 403, Messages: []string{"Authentication error"}})

	h := newAccountHarness(t, s, f)

	h.press("tab", "L")
	h.unsealWith(passphrase)

	h.assertShows("Re-listing cloudflare.work failed", "not authorized", "work.example", "work.test")
	h.assertHides("work.new")

	if names := zoneNames(t, s); !slices.Equal(names, []string{"work.example", "work.test"}) {
		t.Fatalf("cached Zones after a failed re-listing = %v", names)
	}
}

func TestRelistingNoZonesKeepsTheCacheAndHintsAtTheTokenPermission(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	workExample := zoneNamed(t, s, "work.example")
	if err := s.ReplaceRecords(context.Background(), workExample.ID, []dns.Record{
		{ProviderID: "rw1", Type: "A", Name: "work.example", Value: "192.0.2.80", TTL: 300},
	}); err != nil {
		t.Fatal(err)
	}

	// A token that may not list Zones lists none.
	h := newAccountHarness(t, s, fake.New())

	h.press("tab", "L")
	h.unsealWith(passphrase)

	h.assertShows("no Zones listed, cache kept", "Check that its API token may list Zones", "work.example", "work.test")

	if names := zoneNames(t, s); !slices.Equal(names, []string{"work.example", "work.test"}) {
		t.Fatalf("cached Zones after re-listing no Zones = %v", names)
	}

	if records, err := s.Records(context.Background(), workExample.ID); err != nil || len(records) != 1 {
		t.Fatalf("Records after re-listing no Zones = %+v, %v", records, err)
	}
}

func TestARelistingThatFinishesAfterItsAccountWasRemovedIsDropped(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := fake.New().AddZone(dns.Zone{Name: "work.new", ProviderID: "zw3"})
	h := newAccountHarness(t, s, f)

	// Unseal first, so the re-listing starts at once.
	h.press("tab", "X")
	h.unsealWith(passphrase)
	h.press("esc")

	relist := h.pressHolding("L")
	h.assertShows("Re-listing the Zones of cloudflare.work…")

	h.press("X")
	h.typeText("cloudflare.work")
	h.press("enter")
	h.assertShows("Removed cloudflare.work.")

	h.run(relist)

	h.assertShows("Removed cloudflare.work.")
	h.assertHides("failed", "FOREIGN KEY", "work.new")

	if names := zoneNames(t, s); len(names) != 0 {
		t.Fatalf("the late re-listing brought back Zones: %v", names)
	}
}

func TestRelistingNeedsTheAccountsPane(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := workZones()
	h := newAccountHarness(t, s, f)

	h.press("L")

	h.assertShows("Highlight an Account in the Accounts pane first.")
	h.assertHides("Passphrase")

	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("provider calls = %+v", calls)
	}
}
