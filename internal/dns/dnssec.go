package dns

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	mdns "github.com/miekg/dns"
)

// SigningKey is a key-signing key of a Zone: its DNSKEY, and the Delegation
// Signer (DS) records that fix it at the parent zone, each in presentation
// form after the type.
//
// It is an interim type, until the dnsprovider module has its own.
type SigningKey struct {
	// DNSKEY is the key, e.g. "257 3 13 <base64>".
	DNSKEY string
	// KeyTag is the key tag that names the key, as its DS records do.
	KeyTag uint16
	// DS are its DS records, SHA-256 first, e.g. "2371 13 2 1F98…".
	DS []string
}

// SigningKeyOf returns the SigningKey of a DNSKEY record, with its SHA-256
// DS, computed for the zone the record's owner name names.
func SigningKeyOf(k *mdns.DNSKEY) (SigningKey, error) {
	ds := k.ToDS(mdns.SHA256)
	if ds == nil {
		return SigningKey{}, fmt.Errorf("cannot compute the DS of the key with tag %d", k.KeyTag())
	}

	return SigningKey{
		DNSKEY: fmt.Sprintf("%d %d %d %s", k.Flags, k.Protocol, k.Algorithm, k.PublicKey),
		KeyTag: k.KeyTag(),
		DS:     []string{DSText(ds)},
	}, nil
}

// DSText is a DS record in presentation form after the type, its digest
// in upper case, e.g. "2371 13 2 1F98…": the form SigningKey and the DS
// Check hold DS records in.
func DSText(ds *mdns.DS) string {
	return fmt.Sprintf("%d %d %d %s", ds.KeyTag, ds.Algorithm, ds.DigestType, strings.ToUpper(ds.Digest))
}

// Matches reports whether the DS record ds, in presentation form after the
// type, fixes k for zone: the digest of the same type computed from k is
// the same, whatever the case of its hex digits.
func (k SigningKey) Matches(zone, ds string) bool {
	f := strings.Fields(ds)
	if len(f) != 4 {
		return false
	}

	digestType, err := strconv.ParseUint(f[2], 10, 8)
	if err != nil {
		return false
	}

	rr, err := mdns.NewRR(mdns.Fqdn(zone) + " IN DNSKEY " + k.DNSKEY)
	if err != nil {
		return false
	}

	key, ok := rr.(*mdns.DNSKEY)
	if !ok {
		return false
	}

	want := key.ToDS(uint8(digestType))

	return want != nil && DSText(want) == strings.Join([]string{f[0], f[1], f[2], strings.ToUpper(f[3])}, " ")
}

// ErrNameserversDisagree is the error for a Zone's nameservers serving
// different key-signing keys, such as while a newly signed Zone reaches
// each of them.
var ErrNameserversDisagree = errors.New("the nameservers serve different key-signing keys")
