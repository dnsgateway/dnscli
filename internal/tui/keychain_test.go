package tui_test

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// fakeKeychain stands in for the macOS Keychain behind Touch ID.
type fakeKeychain struct {
	items map[string][]byte
	// stored counts the items ever stored, so references stay unique.
	stored int
	// touches counts Touch ID prompts.
	touches int
	// denied makes Touch ID fail, as when the user cancels it.
	denied bool
	// refuse makes Store fail, as on a Mac that cannot protect an item
	// with Touch ID.
	refuse error
}

func (k *fakeKeychain) Store(secret []byte) ([]byte, error) {
	if k.refuse != nil {
		return nil, k.refuse
	}

	if k.items == nil {
		k.items = map[string][]byte{}
	}

	k.stored++
	ref := fmt.Sprintf("item-%d", k.stored)
	k.items[ref] = append([]byte(nil), secret...)

	return []byte(ref), nil
}

func (k *fakeKeychain) Load(ref []byte) ([]byte, error) {
	k.touches++

	if k.denied {
		return nil, errors.New("Touch ID was canceled")
	}

	secret, ok := k.items[string(ref)]
	if !ok {
		return nil, errors.New("the item could not be found in the keychain")
	}

	return secret, nil
}

func (k *fakeKeychain) Delete(ref []byte) error {
	delete(k.items, string(ref))

	return nil
}

// newKeychainHarness starts a session on a Mac: the Keychain is available
// and is the session's Unseal Method.
func newKeychainHarness(t *testing.T, s *store.Store, f *fake.Provider, kc *fakeKeychain) *harness {
	t.Helper()

	keychain := vault.Keychain{Backend: kc}

	return newHarnessWith(t, s, tui.Deps{
		Providers:    provider.Registry{"cloudflare": f.New},
		UnsealMethod: keychain,
		Passphrase:   fastPassphrase(),
		Keychain:     keychain,
	})
}

// newPassphraseOnMacHarness starts a session on a Mac with the passphrase
// chosen as its Unseal Method.
func newPassphraseOnMacHarness(t *testing.T, s *store.Store, f *fake.Provider, kc *fakeKeychain) *harness {
	t.Helper()

	return newHarnessWith(t, s, tui.Deps{
		Providers:    provider.Registry{"cloudflare": f.New},
		UnsealMethod: fastPassphrase(),
		Keychain:     vault.Keychain{Backend: kc},
	})
}

// enrollKeychain enrolls the Keychain in a passphrase session.
func enrollKeychain(t *testing.T, s *store.Store, kc *fakeKeychain) {
	t.Helper()

	h := newPassphraseOnMacHarness(t, s, fake.New(), kc)
	h.press("K")
	h.typeText(passphrase)
	h.press("enter")
	h.press("enter")
	h.assertShows("Keychain enrolled")
}

func TestTheKeychainIsEnrolledFromAnUnsealedSessionAndThenUnsealsWithTouchID(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	kc := &fakeKeychain{}
	h := newPassphraseOnMacHarness(t, s, fake.New(), kc)

	// Enrolling needs the data key, so a sealed session Unseals first.
	h.press("K")
	h.assertShows("Unseal", "Passphrase")
	h.typeText(passphrase)
	h.press("enter")

	h.assertShows("unsealed", "Enroll the Keychain", "Touch ID", "passphrase stays enrolled")
	assertKeySlots(t, s, 1)

	h.press("enter")

	h.assertShows("Keychain enrolled")
	h.assertHides("Enroll the Keychain")
	assertKeySlots(t, s, 2)

	// A later session Unseals with Touch ID: nothing to type.
	f := fake.New().AddZone(dns.Zone{Name: "home.example", ProviderID: "zh1"})
	later := newKeychainHarness(t, s, f, kc)
	later.fillAddAccount("home", "home-token")

	later.assertShows("unsealed", "cloudflare.home", "home.example")
	later.assertHides("Passphrase")

	if kc.touches != 1 {
		t.Fatalf("Touch ID prompted %d times, want 1", kc.touches)
	}

	if calls := f.Calls(); len(calls) == 0 || calls[0].Token != "home-token" {
		t.Fatalf("provider calls = %+v", calls)
	}
}

func TestUnsealWithTheKeychainPromptsForTouchID(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	kc := &fakeKeychain{}
	enrollKeychain(t, s, kc)

	h := newKeychainHarness(t, s, fake.New(), kc)
	h.press("a", "tab")
	h.typeText("home")
	h.press("tab")
	h.typeText("home-token")
	unseal := h.pressHolding("enter")

	h.assertShows("Unseal", "Touch ID")
	h.assertHides("Passphrase")

	h.run(unseal)
	h.assertShows("unsealed", "cloudflare.home")
}

func TestCancellingTheKeychainEnrollmentEnrollsNothing(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newPassphraseOnMacHarness(t, s, fake.New(), &fakeKeychain{})

	h.press("K")
	h.press("esc")
	h.assertShows("sealed · read-only")
	h.assertHides("Enroll the Keychain")

	h.press("K")
	h.typeText(passphrase)
	h.press("enter", "esc")
	h.assertHides("Enroll the Keychain")
	assertKeySlots(t, s, 1)
}

func TestTheKeychainIsNotOfferedWhereItIsUnavailable(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newAccountHarness(t, s, fake.New())

	h.press("K")

	h.assertShows("not available on this platform")
	h.assertHides("Passphrase", "Enroll the Keychain")
	assertKeySlots(t, s, 1)
}

func TestAKeychainSessionWithoutAKeychainKeySlotAsksForThePassphrase(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	kc := &fakeKeychain{}
	h := newKeychainHarness(t, s, fake.New(), kc)

	h.fillAddAccount("home", "home-token")

	h.assertShows("Keychain is not enrolled", "Passphrase")
	h.typeText(passphrase)
	h.press("enter")
	h.assertShows("unsealed", "cloudflare.home")

	if kc.touches != 0 {
		t.Fatalf("Touch ID prompted %d times without a Keychain key slot", kc.touches)
	}
}

func TestTheFirstDataKeyOnAMacIsProtectedByAPassphraseForRecovery(t *testing.T) {
	s, _ := openStore(t)
	h := newKeychainHarness(t, s, workZones(), &fakeKeychain{})

	h.fillAddAccount("work", token)

	h.assertShows("Choose a passphrase")
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")

	h.assertShows("unsealed", "work.example")
	assertKeySlots(t, s, 1)
}

func TestWhenTouchIDFailsThePassphraseStillUnseals(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	kc := &fakeKeychain{}
	enrollKeychain(t, s, kc)

	kc.denied = true
	h := newKeychainHarness(t, s, fake.New(), kc)
	h.fillAddAccount("home", "home-token")

	h.assertShows("Touch ID was canceled", "Passphrase", "sealed · read-only")

	h.typeText(passphrase)
	h.press("enter")
	h.assertShows("unsealed", "cloudflare.home")
}

func TestReenrollingTheKeychainReplacesItsKeySlot(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	kc := &fakeKeychain{}
	enrollKeychain(t, s, kc)

	// The Keychain item was lost, e.g. after re-enrolling a fingerprint.
	clear(kc.items)
	enrollKeychain(t, s, kc)
	assertKeySlots(t, s, 2)

	h := newKeychainHarness(t, s, fake.New(), kc)
	h.fillAddAccount("home", "home-token")
	h.assertShows("unsealed", "cloudflare.home")
}

func TestReenrollingTheKeychainDeletesTheReplacedItem(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	kc := &fakeKeychain{}
	enrollKeychain(t, s, kc)
	enrollKeychain(t, s, kc)

	if len(kc.items) != 1 {
		t.Fatalf("Keychain items = %v, want only the latest one", slices.Collect(maps.Keys(kc.items)))
	}

	h := newKeychainHarness(t, s, fake.New(), kc)
	h.fillAddAccount("home", "home-token")
	h.assertShows("unsealed", "cloudflare.home")
}

func TestTheKeychainIsNotEnrolledWhereTouchIDCannotProtectIt(t *testing.T) {
	// Short reasons, so the dialog does not wrap them.
	for _, why := range []string{"unsigned build", "no Touch ID"} {
		t.Run(why, func(t *testing.T) {
			s, _ := enrollFirstAccount(t)
			kc := &fakeKeychain{refuse: fmt.Errorf("%w: %s", vault.ErrTouchIDUnavailable, why)}
			h := newPassphraseOnMacHarness(t, s, fake.New(), kc)

			h.press("K")
			h.typeText(passphrase)
			h.press("enter", "enter")

			h.assertShows("Touch ID protection is unavailable: "+why, "not enrolled: keep using the passphrase")
			h.assertHides("Keychain enrolled", "Touch ID now Unseals")
			assertKeySlots(t, s, 1)
		})
	}
}

func TestAFailedKeychainEnrollmentLeavesNoKeychainItem(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	kc := &fakeKeychain{}
	h := newPassphraseOnMacHarness(t, s, fake.New(), kc)

	h.press("K")
	h.typeText(passphrase)
	h.press("enter")
	h.assertShows("Enroll the Keychain")

	// The database fails before the key slot is stored.
	_ = s.Close()
	h.press("enter")

	h.assertShows("Could not enroll the Keychain")
	h.assertHides("Keychain enrolled")

	if len(kc.items) != 0 {
		t.Fatalf("Keychain items = %v, want none: the enrollment failed", slices.Collect(maps.Keys(kc.items)))
	}
}
