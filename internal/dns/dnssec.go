package dns

import "errors"

// SigningKey is a key-signing key of a Zone: its DNSKEY, and the Delegation
// Signer (DS) records that fix it at the parent zone, each in presentation
// form after the type.
type SigningKey struct {
	// DNSKEY is the key, e.g. "257 3 13 <base64>".
	DNSKEY string
	// DS are its DS records, SHA-256 first, e.g. "2371 13 2 1F98…".
	DS []string
}

// ErrNameserversDisagree is the error for a Zone's nameservers serving
// different key-signing keys, such as while a newly signed Zone reaches
// each of them.
var ErrNameserversDisagree = errors.New("the nameservers serve different key-signing keys")
