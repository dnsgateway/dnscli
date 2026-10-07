package vault_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sergelogvinov/dnscli/internal/vault"
)

// tamper rewrites one field of a passphrase key slot's stored params.
func tamper(t *testing.T, slot vault.Slot, field string, value any) vault.Slot {
	t.Helper()

	var params map[string]any
	if err := json.Unmarshal(slot.Params, &params); err != nil {
		t.Fatal(err)
	}

	params[field] = value

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	slot.Params = raw

	return slot
}

func TestATamperedPassphraseKeySlotIsRefusedQuickly(t *testing.T) {
	m := vault.Passphrase{Cost: vault.Argon2Cost{Time: 1, MemoryKiB: 8, Threads: 1}}

	_, slot, err := vault.Create(m, []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}

	for name, bad := range map[string]vault.Slot{
		"huge time":    tamper(t, slot, "time", 1<<30),
		"huge memory":  tamper(t, slot, "memory_kib", 1<<31),
		"short salt":   tamper(t, slot, "salt", bytes.Repeat([]byte{1}, 8)),
		"huge salt":    tamper(t, slot, "salt", bytes.Repeat([]byte{1}, 1<<20)),
		"unknown KDF":  tamper(t, slot, "kdf", "scrypt"),
		"zero threads": tamper(t, slot, "threads", 0),
	} {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)

			go func() {
				_, err := m.Unwrap(bad, []byte("pw"))
				done <- err
			}()

			select {
			case err := <-done:
				if err == nil || errors.Is(err, vault.ErrWrongPassphrase) {
					t.Fatalf("Unwrap = %v, want an unsupported-parameters error", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Unwrap is still running after 5s")
			}
		})
	}
}

func TestTheDefaultCostIsWithinTheBounds(t *testing.T) {
	_, slot, err := vault.Create(vault.Passphrase{}, []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := (vault.Passphrase{}).Unwrap(slot, []byte("pw")); err != nil {
		t.Fatalf("Unwrap with the default cost: %v", err)
	}
}
