//go:build darwin && cgo && !devkeychain

package keychain

// devKeychain is off: the Unseal key is stored only where macOS enforces
// Touch ID on it. See devkeychain_on.go.
const devKeychain = false
