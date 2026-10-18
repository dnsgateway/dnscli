// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package vault_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/vault"
)

// fakeKeychain is an in-memory Keychain: each stored secret is one item.
type fakeKeychain struct {
	items map[string][]byte
	// denied makes Load fail, as when the user cancels Touch ID.
	denied bool
}

func (k *fakeKeychain) Store(secret []byte) ([]byte, error) {
	if k.items == nil {
		k.items = map[string][]byte{}
	}

	ref := fmt.Sprintf("item-%d", len(k.items)+1)
	k.items[ref] = append([]byte(nil), secret...)

	return []byte(ref), nil
}

func (k *fakeKeychain) Load(ref []byte) ([]byte, error) {
	if k.denied {
		return nil, errors.New("authentication canceled")
	}

	secret, ok := k.items[string(ref)]
	if !ok {
		return nil, errors.New("item not found")
	}

	return secret, nil
}

func (k *fakeKeychain) Delete(ref []byte) error {
	delete(k.items, string(ref))

	return nil
}

var fastPassphrase = vault.Passphrase{Cost: vault.Argon2Cost{Time: 1, MemoryKiB: 8, Threads: 1}}

func TestAKeychainKeySlotWrapsTheExistingDataKey(t *testing.T) {
	key, passSlot, err := vault.Create(fastPassphrase, []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}

	sealed, err := key.Seal([]byte("token"), "acct")
	if err != nil {
		t.Fatal(err)
	}

	kc := vault.Keychain{Backend: &fakeKeychain{}}

	kcSlot, err := key.Wrap(kc, nil)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}

	if kcSlot.Method != "keychain" {
		t.Fatalf("slot method = %q, want keychain", kcSlot.Method)
	}

	slots := []vault.Slot{passSlot, kcSlot}

	// Either method releases the same data key.
	for _, m := range []struct {
		method vault.Method
		secret []byte
	}{{kc, nil}, {fastPassphrase, []byte("pw")}} {
		k, err := vault.Unseal(m.method, slots, m.secret)
		if err != nil {
			t.Fatalf("Unseal with %s: %v", m.method.Name(), err)
		}

		if got, err := k.Open(sealed, "acct"); err != nil || string(got) != "token" {
			t.Fatalf("Open after Unseal with %s = %q, %v", m.method.Name(), got, err)
		}
	}
}

func TestADeniedKeychainLeavesTheSessionSealed(t *testing.T) {
	backend := &fakeKeychain{}
	kc := vault.Keychain{Backend: backend}

	_, slot, err := vault.Create(kc, nil)
	if err != nil {
		t.Fatal(err)
	}

	backend.denied = true

	if _, err := vault.Unseal(kc, []vault.Slot{slot}, nil); err == nil {
		t.Fatal("Unseal succeeded with Touch ID denied")
	}
}

func TestTheKeychainIsUnavailableWithoutABackend(t *testing.T) {
	if _, _, err := vault.Create(vault.Keychain{}, nil); !errors.Is(err, vault.ErrKeychainUnavailable) {
		t.Fatalf("Create = %v, want ErrKeychainUnavailable", err)
	}
}

func TestOnlyThePassphraseAsksForASecret(t *testing.T) {
	if !vault.AsksForSecret(fastPassphrase) {
		t.Fatal("the passphrase must ask for a secret")
	}

	if vault.AsksForSecret(vault.Keychain{}) {
		t.Fatal("the Keychain asks the user itself, through Touch ID")
	}
}
