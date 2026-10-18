// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

//go:build !darwin || !cgo

package keychain

import "github.com/sergelogvinov/dnscli/internal/vault"

// System returns nil: this platform, or a macOS build without cgo, has no
// Keychain with Touch ID.
func System() vault.KeychainBackend {
	return nil
}
