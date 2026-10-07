// Package vault protects Account secrets (ADR-0005).
//
// A random data key encrypts every secret. Each enrolled Unseal Method
// wraps that data key in its own key slot, like LUKS key slots, so any one
// method releases it. Secrets are decrypted only in memory.
package vault

import (
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// sealedVersion prefixes every sealed blob, so the format can change later.
const sealedVersion = 1

// ErrNoSlot means no key slot is enrolled for the session's Unseal Method.
var ErrNoSlot = errors.New("no key slot is enrolled for this Unseal Method")

// Slot is one Unseal Method's wrapping of the data key.
type Slot struct {
	ID int64
	// Method is the Unseal Method's name, e.g. "passphrase".
	Method string
	// Params are the method's own parameters, such as KDF salt and cost.
	Params []byte
	// Wrapped is the data key, encrypted by the method.
	Wrapped []byte
}

// Method is an Unseal Method: one way to wrap and unwrap the data key.
type Method interface {
	// Name identifies the method's key slots, e.g. "passphrase".
	Name() string
	// Wrap wraps dataKey in a new key slot, using the user's secret
	// (for the passphrase method, the passphrase).
	Wrap(dataKey, secret []byte) (Slot, error)
	// Unwrap releases the data key from one of the method's key slots.
	Unwrap(slot Slot, secret []byte) ([]byte, error)
}

// Key is the released data key. Holding one means the session is unsealed.
type Key struct {
	dataKey []byte
	aead    cipher.AEAD
}

// Create makes a new data key and wraps it in a key slot for m.
func Create(m Method, secret []byte) (*Key, Slot, error) {
	dataKey := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(dataKey); err != nil {
		return nil, Slot{}, fmt.Errorf("create data key: %w", err)
	}

	slot, err := m.Wrap(dataKey, secret)
	if err != nil {
		return nil, Slot{}, err
	}

	k, err := newKey(dataKey)

	return k, slot, err
}

// Unseal releases the data key from the first of m's key slots that the
// secret opens. It returns ErrNoSlot when m has no key slot, and m's own
// error, such as ErrWrongPassphrase, when none opens.
func Unseal(m Method, slots []Slot, secret []byte) (*Key, error) {
	err := ErrNoSlot

	for _, s := range slots {
		if s.Method != m.Name() {
			continue
		}

		var dataKey []byte
		if dataKey, err = m.Unwrap(s, secret); err == nil {
			return newKey(dataKey)
		}
	}

	return nil, err
}

func newKey(dataKey []byte) (*Key, error) {
	aead, err := chacha20poly1305.NewX(dataKey)
	if err != nil {
		return nil, fmt.Errorf("data key: %w", err)
	}

	return &Key{dataKey: dataKey, aead: aead}, nil
}

// Wrap wraps the session's data key in a new key slot for m, so m Unseals
// the same secrets as the method that released it. Only an unsealed
// session holds a Key, so only an unsealed session can enroll a method.
func (k *Key) Wrap(m Method, secret []byte) (Slot, error) {
	return m.Wrap(k.dataKey, secret)
}

// AsksForSecret reports whether dnscli asks the user for m's secret, as
// for the passphrase. A method such as the Keychain asks the user itself.
func AsksForSecret(m Method) bool {
	_, own := m.(interface{ authenticatesItself() })

	return !own
}

// Seal encrypts a secret. The label binds it to its owner, such as an
// Account, so a sealed secret cannot be moved to another owner.
func (k *Key) Seal(plaintext []byte, label string) ([]byte, error) {
	return seal(k.aead, plaintext, []byte(label))
}

// Open decrypts a secret sealed with the same label.
func (k *Key) Open(sealed []byte, label string) ([]byte, error) {
	plaintext, err := open(k.aead, sealed, []byte(label))
	if err != nil {
		return nil, fmt.Errorf("decrypt secret: %w", err)
	}

	return plaintext, nil
}

// seal encrypts with XChaCha20-Poly1305 and a random nonce, as
// version || nonce || ciphertext.
func seal(aead cipher.AEAD, plaintext, ad []byte) ([]byte, error) {
	out := make([]byte, 1+aead.NonceSize(), 1+aead.NonceSize()+len(plaintext)+aead.Overhead())
	out[0] = sealedVersion

	if _, err := rand.Read(out[1:]); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}

	return aead.Seal(out, out[1:], plaintext, ad), nil
}

func open(aead cipher.AEAD, sealed, ad []byte) ([]byte, error) {
	n := aead.NonceSize()
	if len(sealed) < 1+n+aead.Overhead() || sealed[0] != sealedVersion {
		return nil, errors.New("malformed sealed data")
	}

	return aead.Open(nil, sealed[1:1+n], sealed[1+n:], ad)
}
