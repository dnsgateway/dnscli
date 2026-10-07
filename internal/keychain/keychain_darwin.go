//go:build darwin && cgo

package keychain

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework Security -framework LocalAuthentication
#include <stdlib.h>
#include "keychain_darwin.h"
*/
import "C"

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"

	"github.com/sergelogvinov/dnscli/internal/vault"
)

// touchIDReason completes macOS's "dnscli is trying to …" Touch ID prompt.
const touchIDReason = "unseal your DNS Account secrets"

// System returns the macOS Keychain with Touch ID.
func System() vault.KeychainBackend {
	return darwinKeychain{}
}

type darwinKeychain struct{}

// itemRef locates a Keychain item. The key slot stores it.
type itemRef struct {
	// Account is the item's account attribute; the service is "dnscli".
	Account string `json:"account"`
	// DataProtection means the item is in the data protection keychain,
	// where its access control enforces Touch ID. Otherwise it is in the
	// login keychain, and dnscli asks for Touch ID before reading it.
	DataProtection bool `json:"data_protection"`
}

// Store implements vault.KeychainBackend.
func (darwinKeychain) Store(secret []byte) ([]byte, error) {
	if len(secret) == 0 {
		return nil, errors.New("empty secret")
	}

	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}

	ref := itemRef{Account: "unseal-" + hex.EncodeToString(id)}

	account := C.CString(ref.Account)
	defer C.free(unsafe.Pointer(account))

	var (
		dataProtection C.int
		msg            *C.char
	)

	if status := C.dnscli_keychain_store(account, unsafe.Pointer(&secret[0]), C.int(len(secret)), &dataProtection, &msg); status != 0 {
		return nil, keychainError(status, msg)
	}

	ref.DataProtection = dataProtection != 0

	return json.Marshal(ref)
}

// Load implements vault.KeychainBackend. It shows the Touch ID prompt.
func (darwinKeychain) Load(raw []byte) ([]byte, error) {
	var ref itemRef
	if err := json.Unmarshal(raw, &ref); err != nil || ref.Account == "" {
		return nil, fmt.Errorf("unreadable Keychain item reference: %v", err)
	}

	account := C.CString(ref.Account)
	defer C.free(unsafe.Pointer(account))

	reason := C.CString(touchIDReason)
	defer C.free(unsafe.Pointer(reason))

	var dataProtection C.int
	if ref.DataProtection {
		dataProtection = 1
	}

	var (
		secret unsafe.Pointer
		n      C.int
		msg    *C.char
	)

	if status := C.dnscli_keychain_load(account, dataProtection, reason, &secret, &n, &msg); status != 0 {
		return nil, keychainError(status, msg)
	}
	defer C.free(secret)

	return C.GoBytes(secret, n), nil
}

// keychainError turns a bridge result into an error, freeing its message.
func keychainError(status C.int, msg *C.char) error {
	text := fmt.Sprintf("OSStatus %d", int(status))
	if msg != nil {
		text = C.GoString(msg)
		C.free(unsafe.Pointer(msg))
	}

	switch status {
	case C.DNSCLI_KEYCHAIN_CANCELED:
		return fmt.Errorf("Touch ID was canceled (%s)", text)
	case C.DNSCLI_KEYCHAIN_NO_BIOMETRY:
		return fmt.Errorf("Touch ID is not available: %s", text)
	case C.DNSCLI_KEYCHAIN_AUTH_FAILED:
		return fmt.Errorf("Touch ID failed: %s", text)
	}

	return errors.New(text)
}
