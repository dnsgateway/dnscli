// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/tui"
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

	f := newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.new", ProviderID: "zw3"})
	h := newAccountHarness(t, s, f)

	h.press("shift+tab", "j", "L")

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

	// The listed Zones carry no nameservers, so the Account's are asked for.
	calls := f.Calls()
	if len(calls) != 3 || calls[0].Op != providertest.OpNew || calls[0].Fields["token"] != token || calls[1].Op != providertest.OpListZones || calls[2].Op != providertest.OpAccount {
		t.Fatalf("provider calls = %+v", calls)
	}
}

func TestAFailedRelistingLeavesTheCacheAndShowsTheError(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	f := newFake().AddZone(dns.Zone{Name: "work.new", ProviderID: "zw3"})
	f.Fail(providertest.OpListZones, &dnsprovider.APIError{Provider: "cloudflare", StatusCode: 403, Messages: []string{"Authentication error"}})

	h := newAccountHarness(t, s, f)

	h.press("shift+tab", "L")
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
	h := newAccountHarness(t, s, newFake())

	h.press("shift+tab", "L")
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
	f := newFake().AddZone(dns.Zone{Name: "work.new", ProviderID: "zw3"})
	h := newAccountHarness(t, s, f)

	// Unseal first, so the re-listing starts at once.
	h.press("shift+tab", "d")
	h.unsealWith(passphrase)
	h.press("esc")

	relist := h.pressHolding("L")
	h.assertShows("Re-listing the Zones of cloudflare.work…")

	h.press("d")
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

func TestRelistingThatDropsTheOpenZoneClosesItsTemplateDialog(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)

	// The re-listing is started from the Accounts pane and finishes while
	// the Template dialog is open.
	h.press("shift+tab", "shift+tab")
	relist := h.pressHolding("L")
	h.press("shift+tab", "t")
	h.assertShows("Apply a Template to work.example")

	f.RemoveZone("zw1")
	h.run(relist)

	h.assertShows("1 dropped")
	h.assertHides("Apply a Template")
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

// zonesCalls counts the Provider's Zones listings.
func zonesCalls(f *fakeProvider) int {
	n := 0

	for _, c := range f.Calls() {
		if c.Op == providertest.OpListZones {
			n++
		}
	}

	return n
}

func TestRInTheDomainsPaneReListsEveryAccountWhenNoneIsShown(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	f := workZones()
	h := newAccountHarness(t, s, f)
	h.fillAddAccount("home", "home-token")
	h.unsealWith(passphrase)
	h.assertShows("cloudflare.home")

	before := zonesCalls(f)

	h.press("tab", "r") // every Account's Zones, in the Domains pane

	if n := zonesCalls(f) - before; n != 2 {
		t.Fatalf("Zones listings = %d, want one per Account: %+v", n, f.Calls())
	}

	h.assertShows("Re-listed 2 Accounts: no changes.")
}

func TestReListingEveryAccountReportsEachFailureOnceAllAreBack(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	f := workZones()
	h := newAccountHarness(t, s, f)
	h.fillAddAccount("home", "home-token")
	h.unsealWith(passphrase)

	f.Fail(providertest.OpListZones, errors.New("connection reset by peer"))
	h.press("tab", "r")

	h.assertShows("Re-listing cloudflare.home failed: connection reset by peer. Re-listing cloudflare.work failed")
}

func TestRInTheAccountsPaneReListsOnlyTheAccountShown(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	withPersonalAccount(t, s)

	f := workZones()
	h := newAccountHarness(t, s, f)

	h.press("shift+tab", "j", "space", "k", "r") // cloudflare.work shown, cloudflare.personal highlighted
	h.unsealWith(passphrase)

	h.assertShows("Re-listed cloudflare.work")

	if n := zonesCalls(f); n != 1 {
		t.Fatalf("Zones listings = %d, want 1: %+v", n, f.Calls())
	}
}

func TestRelistingStoresEachZonesHostingState(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	if got := zoneNamed(t, s, "work.example").State; got != dnsprovider.HostingActive {
		t.Fatalf("work.example after discovery = %v, want active", got)
	}

	f := newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2"}).
		AddZone(dns.Zone{Name: "work.new", ProviderID: "zw3"})
	f.SetHostingState("zw1", dnsprovider.HostingMoved)
	f.SetHostingState("zw3", dnsprovider.HostingPending)

	h := newAccountHarness(t, s, f)
	h.press("shift+tab", "L")
	h.unsealWith(passphrase)
	h.assertShows("Re-listed cloudflare.work")

	for name, want := range map[string]dnsprovider.HostingState{
		"work.example": dnsprovider.HostingMoved,
		"work.test":    dnsprovider.HostingActive,
		"work.new":     dnsprovider.HostingPending,
	} {
		if got := zoneNamed(t, s, name).State; got != want {
			t.Errorf("%s after re-listing = %v, want %v", name, got, want)
		}
	}
}

// An unscoped Cloudflare token can list a zone name once per Tenant it
// reaches: the cache keeps the one the Provider serves.
func TestAZoneNameListedTwiceIsCachedInItsServedHostingState(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	f := newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw8"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw9"})
	f.SetHostingState("zw8", dnsprovider.HostingMoved)
	f.SetHostingState("zw2", dnsprovider.HostingMoved)
	f.SetHostingState("zw9", dnsprovider.HostingPending)

	h := newAccountHarness(t, s, f)
	h.press("shift+tab", "L")
	h.unsealWith(passphrase)
	h.assertShows("Re-listed cloudflare.work")

	if z := zoneNamed(t, s, "work.example"); z.ProviderID != "zw1" || z.State != dnsprovider.HostingActive {
		t.Errorf("work.example = %+v, want zw1 active", z)
	}

	if z := zoneNamed(t, s, "work.test"); z.ProviderID != "zw9" || z.State != dnsprovider.HostingPending {
		t.Errorf("work.test = %+v, want zw9 pending", z)
	}
}

// A zone name the Provider now lists under another zone ID is another
// zone: nothing this session built on the old one may reach it.
func TestRelistingThatReplacesAZonesProviderIDForgetsItsChangesetAndValidation(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.buildChangeset()

	f.RemoveZone("zw1")
	f.AddZone(dns.Zone{Name: "work.example", ProviderID: "zw7"})

	h.press("shift+tab", "shift+tab", "L")

	h.assertShows("Re-listed cloudflare.work: no Zones added, 0 dropped, 1 replaced by a new zone; its cache and changes were reset.")
	h.assertHides("pending change", "203.0.113.20", "_verify.work.example")

	if z := zoneNamed(t, h.store, "work.example"); z.ProviderID != "zw7" {
		t.Fatalf("work.example = %+v, want zw7", z)
	}

	// The new zone was never Refreshed: it is not validated.
	if row := h.domainRow("work.example"); strings.Contains(row, "✓") {
		t.Fatalf("work.example row = %q, still validated", row)
	}
}

// Of several copies of a zone name in the same Hosting State, the cache
// keeps the one it already holds, else the smallest Provider zone ID, so
// the Provider's listing order does not swap the cached Zone.
func TestATieBetweenCopiesOfAZoneNameKeepsTheCachedZoneElseTheSmallestID(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	f := newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw9"}).
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw5"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw3"})
	for _, id := range []string{"zw9", "zw1", "zw5", "zw3"} {
		f.SetHostingState(id, dnsprovider.HostingMoved)
	}

	h := newAccountHarness(t, s, f)
	h.press("shift+tab", "L")
	h.unsealWith(passphrase)
	h.assertShows("Re-listed cloudflare.work")

	if z := zoneNamed(t, s, "work.example"); z.ProviderID != "zw1" {
		t.Errorf("work.example = %+v, want the cached zw1", z)
	}

	// work.test was cached as zw2, which is no longer listed.
	if z := zoneNamed(t, s, "work.test"); z.ProviderID != "zw3" {
		t.Errorf("work.test = %+v, want zw3, the smallest ID", z)
	}
}

// Zone names compare as DNS names: case and a trailing dot do not make
// another zone name.
func TestCopiesOfAZoneNameSpelledDifferentlyAreCachedOnce(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	f := newFake().
		AddZone(dns.Zone{Name: "Work.Example.", ProviderID: "zw8"}).
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1"}).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2"})
	f.SetHostingState("zw8", dnsprovider.HostingMoved)

	h := newAccountHarness(t, s, f)
	h.press("shift+tab", "L")
	h.unsealWith(passphrase)
	h.assertShows("Re-listed cloudflare.work: no changes, 2 Zones.")

	if names := zoneNames(t, s); !slices.Equal(names, []string{"work.example", "work.test"}) {
		t.Fatalf("cached Zones = %v", names)
	}

	if z := zoneNamed(t, s, "work.example"); z.ProviderID != "zw1" || z.State != dnsprovider.HostingActive {
		t.Errorf("work.example = %+v, want zw1 active", z)
	}
}

// One Tenant can hold a Moved copy of a zone name and the copy Onboarded
// again: a scoped Account caches the one waiting for Delegation.
func TestAScopedAccountCachesTheOnboardedAgainCopyOfAMovedZone(t *testing.T) {
	f := newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw8"}).
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw9"})
	f.SetHostingState("zw8", dnsprovider.HostingMoved)
	f.SetHostingState("zw9", dnsprovider.HostingPending)
	f.SetTenants(dnsprovider.Tenant{ID: acmeTenant, Name: "Acme Ltd"})

	h, s := newTenantHarness(t, f)
	h.fillAddAccount("work", "tok")
	h.enrollPassphrase()
	h.assertShows("Discovered 1 Zone for cloudflare.work.")

	if got := storedFields(t, s, accountLabeled(t, s, "cloudflare.work"))["account"]; got != acmeTenant {
		t.Fatalf("Tenant = %q, want the Account scoped to %s", got, acmeTenant)
	}

	if z := zoneNamed(t, s, "work.example"); z.ProviderID != "zw9" || z.State != dnsprovider.HostingPending {
		t.Errorf("work.example = %+v, want zw9 pending", z)
	}
}

func TestRelistingAnAccountReachingSeveralTenantsListsTheZonesOfEveryOne(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)

	s, _ := enrollFirstAccount(t)
	old := addOldAccount(t, s, "cloudflare", "old", `{"token":"tok","account":"b2"}`)

	tb := &tenantBuilds{}
	h := newHarnessWith(t, s, tui.Deps{Providers: f.tenantRegistry(tb), UnsealMethod: fastPassphrase()})

	h.press("shift+tab", "L")
	h.unsealWith(passphrase)
	h.assertShows("Re-listed cloudflare.old")

	// Opened within its Tenant, then built unscoped, as Cloudflare lists
	// the Zones of every account a token reaches, to list its Zones.
	tb.mu.Lock()
	builds := slices.Clone(tb.tenants)
	tb.mu.Unlock()

	if !slices.Equal(builds, []string{"b2", ""}) {
		t.Fatalf("built with Tenants %q, want b2, then unscoped", builds)
	}

	// Its own Tenant stays the one it Onboards in.
	if got := storedFields(t, s, old)["account"]; got != "b2" {
		t.Fatalf("stored Tenant = %q, want b2 kept", got)
	}
}
