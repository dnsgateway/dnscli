//go:build !darwin || !cgo

package keychain

import "github.com/sergelogvinov/dnscli/internal/vault"

// System returns nil: this platform, or a macOS build without cgo, has no
// Keychain with Touch ID.
func System() vault.KeychainBackend {
	return nil
}
