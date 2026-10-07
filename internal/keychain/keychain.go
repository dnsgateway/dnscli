// Package keychain is the macOS Keychain with Touch ID, the backend of the
// Keychain Unseal Method (vault.Keychain). Other platforms, and macOS
// builds without cgo, have none: System returns nil there.
package keychain
