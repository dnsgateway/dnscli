// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

//go:build darwin && cgo && !devkeychain

package keychain

// devKeychain is off: the Unseal key is stored only where macOS enforces
// Touch ID on it. See devkeychain_on.go.
const devKeychain = false
