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

// itemRef locates a Keychain item in the data protection keychain. The key
// slot stores it.
type itemRef struct {
	// Account is the item's account attribute; the service is "dnscli".
	Account string `json:"account"`
}

// Store implements vault.KeychainBackend. It stores the secret only where
// the OS enforces Touch ID on the item, and otherwise fails with
// vault.ErrTouchIDUnavailable.
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

	var msg *C.char

	if status := C.dnscli_keychain_store(account, unsafe.Pointer(&secret[0]), C.int(len(secret)), &msg); status != 0 {
		text := message(status, msg)

		switch status {
		case C.DNSCLI_KEYCHAIN_NO_BIOMETRY:
			return nil, fmt.Errorf("%w: Touch ID is not set up on this Mac (%s)", vault.ErrTouchIDUnavailable, text)
		case C.DNSCLI_KEYCHAIN_NOT_ENTITLED:
			return nil, fmt.Errorf("%w: this dnscli build is not signed with a keychain entitlement", vault.ErrTouchIDUnavailable)
		case C.DNSCLI_KEYCHAIN_NO_PROTECTION:
			return nil, fmt.Errorf("%w: %s", vault.ErrTouchIDUnavailable, text)
		}

		return nil, errors.New(text)
	}

	return json.Marshal(ref)
}

// Load implements vault.KeychainBackend. It shows the Touch ID prompt.
func (darwinKeychain) Load(raw []byte) ([]byte, error) {
	ref, err := parseRef(raw)
	if err != nil {
		return nil, err
	}

	account := C.CString(ref.Account)
	defer C.free(unsafe.Pointer(account))

	reason := C.CString(touchIDReason)
	defer C.free(unsafe.Pointer(reason))

	var (
		secret unsafe.Pointer
		n      C.int
		msg    *C.char
	)

	if status := C.dnscli_keychain_load(account, reason, &secret, &n, &msg); status != 0 {
		return nil, loadError(status, message(status, msg))
	}
	defer C.free(secret)

	return C.GoBytes(secret, n), nil
}

// Delete implements vault.KeychainBackend. It asks for no Touch ID.
func (darwinKeychain) Delete(raw []byte) error {
	ref, err := parseRef(raw)
	if err != nil {
		return err
	}

	account := C.CString(ref.Account)
	defer C.free(unsafe.Pointer(account))

	var msg *C.char

	if status := C.dnscli_keychain_delete(account, &msg); status != 0 {
		return errors.New(message(status, msg))
	}

	return nil
}

func parseRef(raw []byte) (itemRef, error) {
	var ref itemRef
	if err := json.Unmarshal(raw, &ref); err != nil || ref.Account == "" {
		return itemRef{}, fmt.Errorf("unreadable Keychain item reference: %v", err)
	}

	return ref, nil
}

// message returns a bridge result's message, freeing it.
func message(status C.int, msg *C.char) string {
	if msg == nil {
		return fmt.Sprintf("OSStatus %d", int(status))
	}

	defer C.free(unsafe.Pointer(msg))

	return C.GoString(msg)
}

// loadError explains why reading an item failed.
func loadError(status C.int, text string) error {
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
