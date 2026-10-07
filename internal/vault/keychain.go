package vault

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// ErrKeychainUnavailable means this platform or build has no Keychain.
var ErrKeychainUnavailable = errors.New("the Keychain Unseal Method is not available on this platform")

// ErrTouchIDUnavailable means the OS cannot make Touch ID guard a keychain
// item on this machine, such as for a build without the keychain
// entitlement or on a Mac without Touch ID. A backend stores nothing then:
// an item dnscli alone guards would be no better than the passphrase.
var ErrTouchIDUnavailable = errors.New("Touch ID protection is unavailable")

// KeychainBackend keeps secrets in the OS keychain, such as the macOS
// Keychain behind Touch ID.
type KeychainBackend interface {
	// Store saves secret as a new keychain item that only the user's
	// presence, such as Touch ID, releases. It returns a reference to the
	// item, which the key slot keeps.
	Store(secret []byte) (ref []byte, err error)
	// Load asks the user to authenticate, such as with Touch ID, and
	// returns the item's secret.
	Load(ref []byte) ([]byte, error)
	// Delete removes the item, without asking the user. An item that is
	// already gone is not an error.
	Delete(ref []byte) error
}

// Keychain is the Keychain Unseal Method. A random key-encryption key lives
// in the OS keychain, released only by Touch ID; the key slot holds the
// data key wrapped with it. dnscli asks for no secret: the OS asks the user.
type Keychain struct {
	// Backend is the OS keychain; nil where the platform has none.
	Backend KeychainBackend
}

// keychainParams are stored with each Keychain key slot.
type keychainParams struct {
	// Item references the keychain item that holds the key-encryption key.
	Item []byte `json:"item"`
}

// Name implements Method.
func (Keychain) Name() string { return "keychain" }

func (Keychain) authenticatesItself() {}

// Wrap implements Method. The secret is unused.
func (k Keychain) Wrap(dataKey, _ []byte) (Slot, error) {
	if k.Backend == nil {
		return Slot{}, ErrKeychainUnavailable
	}

	kek := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(kek); err != nil {
		return Slot{}, fmt.Errorf("keychain key: %w", err)
	}

	ref, err := k.Backend.Store(kek)
	switch {
	case errors.Is(err, ErrTouchIDUnavailable):
		return Slot{}, err // already says why, for the user
	case err != nil:
		return Slot{}, fmt.Errorf("store the key in the Keychain: %w", err)
	}

	slot, err := k.wrapWith(kek, ref, dataKey)
	if err != nil {
		// The slot was never made, so nothing else references the item.
		_ = k.Backend.Delete(ref)

		return Slot{}, err
	}

	return slot, nil
}

func (k Keychain) wrapWith(kek, ref, dataKey []byte) (Slot, error) {
	raw, err := json.Marshal(keychainParams{Item: ref})
	if err != nil {
		return Slot{}, err
	}

	aead, err := chacha20poly1305.NewX(kek)
	if err != nil {
		return Slot{}, err
	}

	// The params are authenticated with the wrapped key.
	wrapped, err := seal(aead, dataKey, raw)
	if err != nil {
		return Slot{}, err
	}

	return Slot{Method: k.Name(), Params: raw, Wrapped: wrapped}, nil
}

// discard deletes the Keychain item a key slot references.
func (k Keychain) discard(slot Slot) error {
	if k.Backend == nil {
		return ErrKeychainUnavailable
	}

	var params keychainParams
	if err := json.Unmarshal(slot.Params, &params); err != nil {
		return fmt.Errorf("keychain key slot %d: %w", slot.ID, err)
	}

	if err := k.Backend.Delete(params.Item); err != nil {
		return fmt.Errorf("delete the Keychain item: %w", err)
	}

	return nil
}

// Unwrap implements Method. The OS asks the user to authenticate.
func (k Keychain) Unwrap(slot Slot, _ []byte) ([]byte, error) {
	if k.Backend == nil {
		return nil, ErrKeychainUnavailable
	}

	var params keychainParams
	if err := json.Unmarshal(slot.Params, &params); err != nil {
		return nil, fmt.Errorf("keychain key slot %d: %w", slot.ID, err)
	}

	kek, err := k.Backend.Load(params.Item)
	if err != nil {
		return nil, fmt.Errorf("Keychain: %w", err)
	}

	aead, err := chacha20poly1305.NewX(kek)
	if err != nil {
		return nil, fmt.Errorf("keychain key slot %d: %w", slot.ID, err)
	}

	dataKey, err := open(aead, slot.Wrapped, slot.Params)
	if err != nil {
		return nil, fmt.Errorf("keychain key slot %d does not match its Keychain item", slot.ID)
	}

	return dataKey, nil
}
