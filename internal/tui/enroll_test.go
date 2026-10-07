package tui_test

import (
	"context"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
	"github.com/sergelogvinov/dnscli/internal/store"
)

// secondProcess opens another Store on the same database file, as a second
// dnscli process would.
func secondProcess(t *testing.T, path string) *store.Store {
	t.Helper()

	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	return s
}

func assertKeySlots(t *testing.T, s *store.Store, want int) {
	t.Helper()

	if slots, err := s.KeySlots(context.Background()); err != nil || len(slots) != want {
		t.Fatalf("%d key slots (%v), want %d", len(slots), err, want)
	}
}

func TestEnrollingWhenAnotherSessionEnrolledFirstUnsealsWithItsDataKey(t *testing.T) {
	s1, path := openStore(t)
	s2 := secondProcess(t, path)

	// Both sessions start before either has enrolled a passphrase.
	first := newAccountHarness(t, s1, workZones())
	second := newAccountHarness(t, s2, fake.New().AddZone(dns.Zone{Name: "home.example", ProviderID: "zh1"}))

	first.fillAddAccount("work", token)
	first.typeText(passphrase)
	first.press("tab")
	first.typeText(passphrase)
	first.press("enter")
	first.assertShows("work.example", "unsealed")

	// The second session still believes nothing is enrolled.
	second.fillAddAccount("home", "home-token")
	second.assertShows("Choose a passphrase")
	second.typeText(passphrase)
	second.press("tab")
	second.typeText(passphrase)
	second.press("enter")

	second.assertShows("unsealed", "cloudflare.home", "home.example")
	assertKeySlots(t, s1, 1)

	// A later session unseals both Accounts with the one data key.
	f := fake.New()
	later := newAccountHarness(t, s1, f)
	later.fillAddAccount("lab", "lab-token")
	later.typeText(passphrase)
	later.press("enter")
	later.assertShows("unsealed", "cloudflare.lab")
}

func TestADifferentPassphraseCannotEnrollASecondDataKey(t *testing.T) {
	s1, path := openStore(t)
	s2 := secondProcess(t, path)

	first := newAccountHarness(t, s1, workZones())
	second := newAccountHarness(t, s2, fake.New())

	first.fillAddAccount("work", token)
	first.typeText(passphrase)
	first.press("tab")
	first.typeText(passphrase)
	first.press("enter")
	first.assertShows("unsealed")

	second.fillAddAccount("home", "home-token")
	second.typeText("other horse")
	second.press("tab")
	second.typeText("other horse")
	second.press("enter")

	second.assertShows("already enrolled", "still sealed", "sealed · read-only")
	second.assertHides("Choose a passphrase", "Repeat")
	assertKeySlots(t, s1, 1)

	if accounts, _ := s1.Accounts(context.Background()); len(accounts) != 1 {
		t.Fatalf("an Account was stored while sealed: %+v", accounts)
	}

	// The prompt now asks for the enrolled passphrase.
	second.typeText(passphrase)
	second.press("enter")
	second.assertShows("unsealed", "cloudflare.home")
}
