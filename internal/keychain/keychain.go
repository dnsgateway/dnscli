// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package keychain is the macOS Keychain with Touch ID, the backend of the
// Keychain Unseal Method (vault.Keychain). Other platforms, and macOS
// builds without cgo, have none: System returns nil there.
package keychain
