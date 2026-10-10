package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

func TestOnlyOneFirstKeySlotIsEverEnrolled(t *testing.T) {
	for range 10 {
		path := filepath.Join(t.TempDir(), "dnscli.db")

		// Two processes running first-time setup at once, each with its own data key.
		stores := []*store.Store{open(t, path), open(t, path)}

		var wg sync.WaitGroup

		errs := make([]error, len(stores))
		for i, s := range stores {
			wg.Go(func() {
				_, errs[i] = s.EnrollFirstKeySlot(context.Background(), vault.Slot{Method: "passphrase", Params: []byte("{}"), Wrapped: []byte{byte(i)}})
			})
		}

		wg.Wait()

		enrolled := 0

		for _, err := range errs {
			switch {
			case err == nil:
				enrolled++
			case !errors.Is(err, store.ErrAlreadyEnrolled):
				t.Fatalf("enroll: %v", err)
			}
		}

		slots, err := stores[0].KeySlots(context.Background())
		if err != nil || enrolled != 1 || len(slots) != 1 {
			t.Fatalf("%d enrollments succeeded and %d key slots exist (%v), want 1 and 1", enrolled, len(slots), err)
		}
	}
}

func TestAnotherKeySlotNeedsAnEnrolledDataKeyAndReplacesItsMethodsSlot(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))
	ctx := context.Background()

	keychain := vault.Slot{Method: "keychain", Params: []byte("{}"), Wrapped: []byte{1}}

	if _, _, err := s.EnrollKeySlot(ctx, keychain); !errors.Is(err, store.ErrNotEnrolled) {
		t.Fatalf("EnrollKeySlot without a data key = %v, want ErrNotEnrolled", err)
	}

	if _, err := s.EnrollFirstKeySlot(ctx, vault.Slot{Method: "passphrase", Params: []byte("{}"), Wrapped: []byte{0}}); err != nil {
		t.Fatal(err)
	}

	for _, wrapped := range []byte{1, 2} {
		keychain.Wrapped = []byte{wrapped}
		if _, _, err := s.EnrollKeySlot(ctx, keychain); err != nil {
			t.Fatalf("EnrollKeySlot: %v", err)
		}
	}

	slots, err := s.KeySlots(ctx)
	if err != nil || len(slots) != 2 || slots[0].Method != "passphrase" || slots[1].Method != "keychain" || slots[1].Wrapped[0] != 2 {
		t.Fatalf("key slots = %+v, %v; want the passphrase and the latest keychain slot", slots, err)
	}
}

func TestEnrollingAnotherKeySlotReturnsTheSlotsItReplaced(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))
	ctx := context.Background()

	if _, err := s.EnrollFirstKeySlot(ctx, vault.Slot{Method: "passphrase", Params: []byte("{}"), Wrapped: []byte{0}}); err != nil {
		t.Fatal(err)
	}

	first, replaced, err := s.EnrollKeySlot(ctx, vault.Slot{Method: "keychain", Params: []byte(`{"item":"old"}`), Wrapped: []byte{1}})
	if err != nil || len(replaced) != 0 {
		t.Fatalf("first keychain slot: replaced %+v, %v; want nothing replaced", replaced, err)
	}

	_, replaced, err = s.EnrollKeySlot(ctx, vault.Slot{Method: "keychain", Params: []byte(`{"item":"new"}`), Wrapped: []byte{2}})
	if err != nil {
		t.Fatal(err)
	}

	// The caller cleans up what a replaced slot referenced, such as its Keychain item.
	if len(replaced) != 1 || replaced[0].ID != first.ID || string(replaced[0].Params) != `{"item":"old"}` {
		t.Fatalf("replaced = %+v, want only the first keychain slot %+v", replaced, first)
	}
}

func TestUpdatingAnAccountRenamesItAndReplacesItsSecret(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))
	ctx := context.Background()

	work, err := s.AddAccountWithSecret(ctx, dns.Account{Provider: "cloudflare", Name: "work"}, []byte{1})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.AddAccountWithSecret(ctx, dns.Account{Provider: "cloudflare", Name: "home"}, []byte{2}); err != nil {
		t.Fatal(err)
	}

	taken := work
	taken.Name = "home"

	err = s.UpdateAccountWithSecret(ctx, taken, []byte{3})
	if !errors.Is(err, store.ErrAccountExists) || !strings.HasPrefix(err.Error(), "update account") {
		t.Fatalf("renaming onto another Account's label: %v, want an update ErrAccountExists", err)
	}

	office := work
	office.Name = "office"

	if err := s.UpdateAccountWithSecret(ctx, office, []byte{4}); err != nil {
		t.Fatal(err)
	}

	accounts, err := s.Accounts(ctx)
	if err != nil || len(accounts) != 2 || accounts[1] != office {
		t.Fatalf("stored Accounts = %+v, %v", accounts, err)
	}

	if sealed, err := s.AccountSecret(ctx, work.ID); err != nil || string(sealed) != "\x04" {
		t.Fatalf("secret = %v, %v", sealed, err)
	}

	gone := dns.Account{ID: 99, Provider: "cloudflare", Name: "gone"}
	if err := s.UpdateAccountWithSecret(ctx, gone, []byte{5}); !errors.Is(err, store.ErrAccountNotFound) {
		t.Fatalf("updating a removed Account: %v, want ErrAccountNotFound", err)
	}
}

func TestASecretIsReplacedOnlyWhileItIsTheOneRead(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "dnscli.db"))
	ctx := context.Background()

	work, err := s.AddAccountWithSecret(ctx, dns.Account{Provider: "cloudflare", Name: "work"}, []byte{1})
	if err != nil {
		t.Fatal(err)
	}

	if replaced, err := s.ReplaceAccountSecret(ctx, work.ID, []byte{1}, []byte{2}); err != nil || !replaced {
		t.Fatalf("replacing the secret read: %v, %v; want it replaced", replaced, err)
	}

	// Saved meanwhile, as by an edit, the secret is not the one read.
	if replaced, err := s.ReplaceAccountSecret(ctx, work.ID, []byte{1}, []byte{3}); err != nil || replaced {
		t.Fatalf("replacing a secret changed meanwhile: %v, %v; want it kept", replaced, err)
	}

	if sealed, err := s.AccountSecret(ctx, work.ID); err != nil || string(sealed) != "\x02" {
		t.Fatalf("secret = %v, %v; want the first replacement", sealed, err)
	}
}
