//go:build darwin && cgo && devkeychain

package keychain

// devKeychain lets an unsigned build, which macOS refuses the data
// protection keychain, store its Unseal key in the login keychain instead.
// There only dnscli's own Touch ID check guards it, not the OS, so this is
// for testing Touch ID during local development and never for a release.
const devKeychain = true
