package tui_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// zoneNamed returns the cached Zone with the given name, failing the test
// when there is none.
func zoneNamed(t *testing.T, s *store.Store, name string) dns.Zone {
	t.Helper()

	zones, err := s.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	for _, z := range zones {
		if z.Name == name {
			return z
		}
	}

	t.Fatalf("no cached Zone %s in %+v", name, zones)

	return dns.Zone{}
}

func zoneNames(t *testing.T, s *store.Store) []string {
	t.Helper()

	zones, err := s.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, z := range zones {
		names = append(names, z.Name)
	}

	return names
}

// unsealWith types the passphrase into the open Unseal prompt.
func (h *harness) unsealWith(p string) {
	h.t.Helper()

	h.assertShows("Unseal", "Passphrase")
	h.typeText(p)
	h.press("enter")
}

// withPersonalAccount adds cloudflare.personal with one Zone and Record next
// to cloudflare.work, so removing or re-listing one Account can be seen
// leaving the other alone.
func withPersonalAccount(t *testing.T, s *store.Store) dns.Zone {
	t.Helper()

	personal := mustAccount(t, s, "cloudflare", "personal")
	z := mustZone(t, s, dns.Zone{AccountID: personal.ID, Name: "personal.example", ProviderID: "zp1", Serial: 7})

	if err := s.ReplaceRecords(context.Background(), z.ID, []dns.Record{
		{ProviderID: "rp1", Type: "A", Name: "personal.example", Value: "192.0.2.7", TTL: 300},
	}); err != nil {
		t.Fatal(err)
	}

	return z
}

func TestRemovingAnAccountAsksForConfirmationThenDeletesItsZonesRecordsAndSecret(t *testing.T) {
	ctx := context.Background()
	s, _ := enrollFirstAccount(t)
	personalZone := withPersonalAccount(t, s)

	workExample := zoneNamed(t, s, "work.example")
	if err := s.ReplaceRecords(ctx, workExample.ID, []dns.Record{
		{ProviderID: "rw1", Type: "TXT", Name: "work.example", Value: "v=work", TTL: 300},
	}); err != nil {
		t.Fatal(err)
	}

	f := fake.New()
	h := newAccountHarness(t, s, f)

	// Accounts are listed by label: cloudflare.personal, then cloudflare.work.
	h.press("tab", "j", "X")
	h.unsealWith(passphrase)

	h.assertShows("Remove Account", "cloudflare.work", "2 Zones")

	if names := zoneNames(t, s); len(names) != 3 {
		t.Fatalf("the confirmation dialog already removed Zones: %v", names)
	}

	// Enter alone does not confirm: the Account label must be typed.
	h.press("enter")
	h.assertShows("Remove Account", "Type cloudflare.work to confirm.", "nothing was removed")

	h.typeText("cloudflare.work")
	h.press("enter")

	h.assertHides("Remove Account", "work.example", "work.test")
	h.assertShows("Removed cloudflare.work.", "cloudflare.personal", "personal.example")

	// Only the notice still names the Account: the Accounts pane does not.
	if n := strings.Count(h.view(), "cloudflare.work"); n != 1 {
		t.Fatalf("the view names cloudflare.work %d times:\n%s", n, h.view())
	}

	accounts, err := s.Accounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].Label() != "cloudflare.personal" {
		t.Fatalf("stored Accounts = %+v, %v", accounts, err)
	}

	if names := zoneNames(t, s); len(names) != 1 || names[0] != "personal.example" {
		t.Fatalf("cached Zones = %v", names)
	}

	if records, err := s.Records(ctx, workExample.ID); err != nil || len(records) != 0 {
		t.Fatalf("Records of a removed Zone = %+v, %v", records, err)
	}

	if records, err := s.Records(ctx, personalZone.ID); err != nil || len(records) != 1 {
		t.Fatalf("Records of the kept Account = %+v, %v", records, err)
	}

	if _, err := s.AccountSecret(ctx, workExample.AccountID); err == nil {
		t.Fatal("the removed Account's secret is still readable")
	}

	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("removing an Account called the Provider: %+v", calls)
	}
}

func TestRemovingTheLastAccountKeepsTheDataKeyEnrolled(t *testing.T) {
	ctx := context.Background()
	s, _ := enrollFirstAccount(t)

	slots, err := s.KeySlots(ctx)
	if err != nil || len(slots) != 1 {
		t.Fatalf("key slots = %+v, %v", slots, err)
	}

	h := newAccountHarness(t, s, fake.New())

	h.press("tab", "X")
	h.unsealWith(passphrase)
	h.typeText("cloudflare.work")
	h.press("enter")
	h.assertShows("Removed cloudflare.work.")

	if accounts, _ := s.Accounts(ctx); len(accounts) != 0 {
		t.Fatalf("stored Accounts = %+v", accounts)
	}

	after, err := s.KeySlots(ctx)
	if err != nil || len(after) != 1 || after[0].ID != slots[0].ID {
		t.Fatalf("key slots after removing the last Account = %+v, %v; want %+v", after, err, slots)
	}

	// A later session adds an Account by Unsealing with the same
	// passphrase: no new one is chosen, so the data key stays the same.
	later := newAccountHarness(t, s, fake.New().AddZone(dns.Zone{Name: "home.example", ProviderID: "zh1"}))
	later.fillAddAccount("home", "home-token")
	later.assertHides("Choose a passphrase")
	later.unsealWith(passphrase)
	later.assertShows("cloudflare.home", "home.example")
}

func TestCancellingTheRemovalKeepsTheAccountAndTypesBoundKeys(t *testing.T) {
	ctx := context.Background()
	s, _ := enrollFirstAccount(t)
	h := newAccountHarness(t, s, fake.New())

	h.press("tab", "X")
	h.unsealWith(passphrase)

	// q quits, ? opens help and X removes outside the dialog; here they are typed.
	h.typeText("q?X")

	if h.quit {
		t.Fatal("q in the confirmation quit the app")
	}

	h.assertShows("Remove Account", "q?X")
	h.assertHides("toggle help")

	h.press("esc")
	h.assertHides("Remove Account")

	if names := zoneNames(t, s); len(names) != 2 {
		t.Fatalf("a cancelled removal changed the cached Zones: %v", names)
	}

	if accounts, _ := s.Accounts(ctx); len(accounts) != 1 {
		t.Fatalf("a cancelled removal changed the Accounts: %+v", accounts)
	}

	h.press("X", "ctrl+c")

	if !h.quit {
		t.Fatal("Ctrl+C did not quit from the confirmation")
	}
}

func TestRemovingAnAccountNeedsTheAccountsPane(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newAccountHarness(t, s, fake.New())

	h.press("X")

	h.assertShows("Highlight an Account in the Accounts pane first.")
	h.assertHides("Remove Account", "Passphrase")
}

func TestRemovingTheAccountOfTheOpenZoneClosesIt(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	workExample := zoneNamed(t, s, "work.example")
	if err := s.ReplaceRecords(context.Background(), workExample.ID, []dns.Record{
		{ProviderID: "rw1", Type: "TXT", Name: "work.example", Value: "v=work", TTL: 300},
	}); err != nil {
		t.Fatal(err)
	}

	withPersonalAccount(t, s)

	h := newAccountHarness(t, s, fake.New())

	// Unseal first, then open work.example: Zones are listed by name.
	h.press("tab", "X")
	h.unsealWith(passphrase)
	h.press("esc", "shift+tab", "j", "enter")
	h.assertShows("v=work")

	h.press("tab", "j", "X")
	h.typeText("cloudflare.work")
	h.press("enter")

	h.assertShows("Removed cloudflare.work.")
	h.assertHides("v=work", "work.example")
}

func TestAZoneThatReusesARemovedZonesIDGetsItsOwnSerialCheck(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	// work.example is possibly stale; the resolver has no answer for home.example.
	h := newHarnessWith(t, s, tui.Deps{
		Providers:    provider.Registry{"cloudflare": fake.New().AddZone(dns.Zone{Name: "home.example", ProviderID: "zh1"}).New},
		UnsealMethod: fastPassphrase(),
		Resolver:     &fakeResolver{serials: map[string]uint32{"work.example": 5, "work.test": 5}},
	})

	if row := h.domainRow("work.example"); !strings.Contains(row, "!") {
		t.Fatalf("work.example row = %q, want the possibly-stale marker", row)
	}

	h.press("tab", "X")
	h.unsealWith(passphrase)
	h.typeText("cloudflare.work")
	h.press("enter")
	h.assertShows("Removed cloudflare.work.")

	// The emptied table hands home.example the ID work.example had.
	h.fillAddAccount("home", "home-token")
	h.assertShows("cloudflare.home")

	if z := zoneNamed(t, s, "home.example"); z.ID != 1 {
		t.Fatalf("home.example has ID %d; the test needs it to reuse ID 1", z.ID)
	}

	if row := h.domainRow("home.example"); !strings.Contains(row, "?") {
		t.Fatalf("home.example row = %q, want its own failed Serial Check", row)
	}
}
