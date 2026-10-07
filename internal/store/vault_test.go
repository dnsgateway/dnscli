package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

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
			wg.Add(1)

			go func() {
				defer wg.Done()

				_, errs[i] = s.EnrollFirstKeySlot(context.Background(), vault.Slot{Method: "passphrase", Params: []byte("{}"), Wrapped: []byte{byte(i)}})
			}()
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

	if _, err := s.EnrollKeySlot(ctx, keychain); !errors.Is(err, store.ErrNotEnrolled) {
		t.Fatalf("EnrollKeySlot without a data key = %v, want ErrNotEnrolled", err)
	}

	if _, err := s.EnrollFirstKeySlot(ctx, vault.Slot{Method: "passphrase", Params: []byte("{}"), Wrapped: []byte{0}}); err != nil {
		t.Fatal(err)
	}

	for _, wrapped := range []byte{1, 2} {
		keychain.Wrapped = []byte{wrapped}
		if _, err := s.EnrollKeySlot(ctx, keychain); err != nil {
			t.Fatalf("EnrollKeySlot: %v", err)
		}
	}

	slots, err := s.KeySlots(ctx)
	if err != nil || len(slots) != 2 || slots[0].Method != "passphrase" || slots[1].Method != "keychain" || slots[1].Wrapped[0] != 2 {
		t.Fatalf("key slots = %+v, %v; want the passphrase and the latest keychain slot", slots, err)
	}
}
