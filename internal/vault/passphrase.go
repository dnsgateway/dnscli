// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package vault

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// ErrWrongPassphrase means the passphrase opens no key slot.
var ErrWrongPassphrase = errors.New("wrong passphrase")

// Argon2Cost is the argon2id cost of deriving a key from a passphrase.
type Argon2Cost struct {
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
}

// DefaultArgon2 is the cost new passphrase key slots use: the second
// recommended option of RFC 9106 (t=3, 64 MiB, 4 lanes).
var DefaultArgon2 = Argon2Cost{Time: 3, MemoryKiB: 64 * 1024, Threads: 4}

// Bounds on the KDF parameters a stored key slot may ask for, so a tampered
// database cannot make Unseal exhaust memory or run for hours.
const (
	maxArgon2MemoryKiB = 4 * 1024 * 1024
	maxArgon2Time      = 16
	minSaltLen         = 16
	maxSaltLen         = 64
)

// Passphrase is the passphrase Unseal Method. The data key is wrapped with
// a key derived from the passphrase by argon2id, a memory-hard KDF.
type Passphrase struct {
	// Cost is used for new key slots; zero means DefaultArgon2. Existing
	// key slots keep the cost stored with them.
	Cost Argon2Cost
}

// passphraseParams are stored with each passphrase key slot.
type passphraseParams struct {
	KDF  string `json:"kdf"`
	Salt []byte `json:"salt"`
	Argon2Cost
}

// Name implements Method.
func (Passphrase) Name() string { return "passphrase" }

// Wrap implements Method.
func (p Passphrase) Wrap(dataKey, passphrase []byte) (Slot, error) {
	if len(passphrase) == 0 {
		return Slot{}, errors.New("the passphrase is empty")
	}

	params := passphraseParams{KDF: "argon2id", Salt: make([]byte, minSaltLen), Argon2Cost: p.Cost}
	if params.Argon2Cost == (Argon2Cost{}) {
		params.Argon2Cost = DefaultArgon2
	}

	if _, err := rand.Read(params.Salt); err != nil {
		return Slot{}, fmt.Errorf("salt: %w", err)
	}

	raw, err := json.Marshal(params)
	if err != nil {
		return Slot{}, err
	}

	aead, err := params.kek(passphrase)
	if err != nil {
		return Slot{}, err
	}

	wrapped, err := seal(aead, dataKey, raw)
	if err != nil {
		return Slot{}, err
	}

	return Slot{Method: p.Name(), Params: raw, Wrapped: wrapped}, nil
}

// Unwrap implements Method. It returns ErrWrongPassphrase when the
// passphrase does not open the slot.
func (p Passphrase) Unwrap(slot Slot, passphrase []byte) ([]byte, error) {
	var params passphraseParams
	if err := json.Unmarshal(slot.Params, &params); err != nil {
		return nil, fmt.Errorf("passphrase key slot %d: %w", slot.ID, err)
	}

	aead, err := params.kek(passphrase)
	if err != nil {
		return nil, fmt.Errorf("passphrase key slot %d: %w", slot.ID, err)
	}

	// The stored params are authenticated with the wrapped key.
	dataKey, err := open(aead, slot.Wrapped, slot.Params)
	if err != nil {
		return nil, ErrWrongPassphrase
	}

	return dataKey, nil
}

// kek derives the key-encryption key from the passphrase.
func (pp passphraseParams) kek(passphrase []byte) (cipher.AEAD, error) {
	c := pp.Argon2Cost
	if pp.KDF != "argon2id" || c.Time == 0 || c.Time > maxArgon2Time || c.Threads == 0 ||
		c.MemoryKiB < 8*uint32(c.Threads) || c.MemoryKiB > maxArgon2MemoryKiB ||
		len(pp.Salt) < minSaltLen || len(pp.Salt) > maxSaltLen {
		return nil, fmt.Errorf("unsupported KDF parameters %s t=%d m=%dKiB p=%d", pp.KDF, c.Time, c.MemoryKiB, c.Threads)
	}

	key := argon2.IDKey(passphrase, pp.Salt, c.Time, c.MemoryKiB, c.Threads, chacha20poly1305.KeySize)

	return chacha20poly1305.NewX(key)
}
