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
	if err != nil {
		return Slot{}, fmt.Errorf("store the key in the Keychain: %w", err)
	}

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
