// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package resolver

import (
	"context"
	"crypto"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	mdns "github.com/miekg/dns"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// signingKey is a DNSKEY with its private key, for signing a zone's
// DNSKEY RRset in tests.
type signingKey struct {
	rr   *mdns.DNSKEY
	priv crypto.Signer
}

// newKey generates an ECDSA P-256 DNSKEY for zone with the given flags:
// 257 for a key-signing key, 256 for a zone-signing key.
func newKey(t *testing.T, zone string, flags uint16) signingKey {
	t.Helper()

	k := &mdns.DNSKEY{
		Hdr:       mdns.RR_Header{Name: mdns.Fqdn(zone), Rrtype: mdns.TypeDNSKEY, Class: mdns.ClassINET, Ttl: 3600},
		Flags:     flags,
		Protocol:  3,
		Algorithm: mdns.ECDSAP256SHA256,
	}

	priv, err := k.Generate(256)
	if err != nil {
		t.Fatal(err)
	}

	return signingKey{rr: k, priv: priv.(crypto.Signer)}
}

// presentation is a DNSKEY's presentation form after the type, as written
// by hand from its fields.
func (k signingKey) presentation() string {
	return "257 3 13 " + k.rr.PublicKey
}

// keyServer is an in-process authoritative nameserver for one zone that
// answers DNSKEY queries with keys, signed by signer over signed (keys
// when nil), and refuses everything else.
type keyServer struct {
	addr string

	mu sync.Mutex
	do []bool // whether each query asked for DNSSEC records
}

// serveKeys starts a key server on a local UDP port.
func serveKeys(t *testing.T, zone string, keys []signingKey, signer signingKey, signed []signingKey) *keyServer {
	t.Helper()

	return serveKeysSignedAs(t, zone, zone, keys, signer, signed)
}

// serveKeysSignedAs is serveKeys with signerName as the RRSIG's signer
// name, in place of the zone.
func serveKeysSignedAs(t *testing.T, zone, signerName string, keys []signingKey, signer signingKey, signed []signingKey) *keyServer {
	t.Helper()

	if signed == nil {
		signed = keys
	}

	rrset := func(ks []signingKey) []mdns.RR {
		out := make([]mdns.RR, len(ks))
		for i, k := range ks {
			out[i] = k.rr
		}

		return out
	}

	var sig *mdns.RRSIG

	if len(keys) > 0 {
		sig = &mdns.RRSIG{
			Hdr:         mdns.RR_Header{Name: mdns.Fqdn(zone), Rrtype: mdns.TypeRRSIG, Class: mdns.ClassINET, Ttl: 3600},
			TypeCovered: mdns.TypeDNSKEY,
			Algorithm:   signer.rr.Algorithm,
			Labels:      uint8(mdns.CountLabel(mdns.Fqdn(zone))),
			OrigTtl:     3600,
			Expiration:  uint32(time.Now().Add(24 * time.Hour).Unix()),
			Inception:   uint32(time.Now().Add(-time.Hour).Unix()),
			KeyTag:      signer.rr.KeyTag(),
			SignerName:  mdns.Fqdn(signerName),
		}

		if err := sig.Sign(signer.priv, rrset(signed)); err != nil {
			t.Fatal(err)
		}
	}

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	s := &keyServer{addr: pc.LocalAddr().String()}

	handler := mdns.HandlerFunc(func(w mdns.ResponseWriter, q *mdns.Msg) {
		opt := q.IsEdns0()

		s.mu.Lock()
		s.do = append(s.do, opt != nil && opt.Do())
		s.mu.Unlock()

		r := new(mdns.Msg)
		r.SetReply(q)
		r.Authoritative = true

		if q.Question[0].Qtype != mdns.TypeDNSKEY || q.Question[0].Name != mdns.Fqdn(zone) {
			r.Rcode = mdns.RcodeRefused
		} else {
			r.Answer = rrset(keys)
			if sig != nil {
				r.Answer = append(r.Answer, sig)
			}
		}

		r.SetEdns0(4096, true)

		_ = w.WriteMsg(r)
	})

	started := make(chan struct{})
	srv := &mdns.Server{PacketConn: pc, Handler: handler, NotifyStartedFunc: func() { close(started) }}

	go func() { _ = srv.ActivateAndServe() }()

	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })

	return s
}

func (s *keyServer) askedForDNSSEC() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]bool(nil), s.do...)
}

// sha256DS matches a SHA-256 DS for an ECDSA P-256 key in presentation
// form after the type: key tag, algorithm 13, digest type 2 and 64 hex
// digits.
var sha256DS = regexp.MustCompile(`^\d+ 13 2 [0-9A-F]{64}$`)

func TestDNSKEYAtReturnsTheKeySigningKeysEveryNameserverAgreesOn(t *testing.T) {
	ksk := newKey(t, "work.example", 257)
	zsk := newKey(t, "work.example", 256)
	keys := []signingKey{ksk, zsk}

	a := serveKeys(t, "work.example", keys, ksk, nil)
	b := serveKeys(t, "work.example", keys, ksk, nil)

	s := &System{Path: "/nonexistent/resolv.conf"}

	got, err := s.DNSKEYAt(context.Background(), "work.example", []string{a.addr, b.addr})
	if err != nil {
		t.Fatalf("DNSKEYAt: %v", err)
	}

	if len(got) != 1 || got[0].DNSKEY != ksk.presentation() {
		t.Fatalf("DNSKEYAt = %+v, want only the key-signing key %q", got, ksk.presentation())
	}

	if ds := got[0].DS; len(ds) != 1 || !sha256DS.MatchString(ds[0]) || !strings.HasPrefix(ds[0], strconv.Itoa(int(ksk.rr.KeyTag()))+" ") {
		t.Fatalf("DS = %q, want one SHA-256 DS with key tag %d", ds, ksk.rr.KeyTag())
	}

	for _, srv := range []*keyServer{a, b} {
		if do := srv.askedForDNSSEC(); len(do) != 1 || !do[0] {
			t.Fatalf("queries asked for DNSSEC records: %v, want one query that does", do)
		}
	}
}

func TestDNSKEYAtFailsNamingTheNameserverThatDisagrees(t *testing.T) {
	ksk := newKey(t, "work.example", 257)
	other := newKey(t, "work.example", 257)

	a := serveKeys(t, "work.example", []signingKey{ksk}, ksk, nil)
	b := serveKeys(t, "work.example", []signingKey{other}, other, nil)

	s := &System{}

	got, err := s.DNSKEYAt(context.Background(), "work.example", []string{a.addr, b.addr})
	if err == nil {
		t.Fatalf("DNSKEYAt = %+v, want an error", got)
	}

	if !errors.Is(err, dns.ErrNameserversDisagree) || !strings.Contains(err.Error(), b.addr) {
		t.Fatalf("DNSKEYAt error = %v, want the nameservers to disagree, naming %s", err, b.addr)
	}
}

func TestDNSKEYAtFailsWhenTheKeysDoNotValidateUnderTheirOwnSignature(t *testing.T) {
	ksk := newKey(t, "work.example", 257)
	zsk := newKey(t, "work.example", 256)

	good := serveKeys(t, "work.example", []signingKey{ksk, zsk}, ksk, nil)
	// The signature covers only the key-signing key, not the set served.
	bad := serveKeys(t, "work.example", []signingKey{ksk, zsk}, ksk, []signingKey{ksk})

	s := &System{}

	got, err := s.DNSKEYAt(context.Background(), "work.example", []string{good.addr, bad.addr})
	if err == nil {
		t.Fatalf("DNSKEYAt = %+v, want an error", got)
	}

	if errors.Is(err, dns.ErrNameserversDisagree) || !strings.Contains(err.Error(), bad.addr) {
		t.Fatalf("DNSKEYAt error = %v, want a bad signature, naming %s", err, bad.addr)
	}
}

func TestDNSKEYAtFailsWhenTheSignatureNamesAnotherSigner(t *testing.T) {
	ksk := newKey(t, "work.example", 257)

	// The keys are signed by their own key, but under the parent's name.
	bad := serveKeysSignedAs(t, "work.example", "example", []signingKey{ksk}, ksk, nil)

	s := &System{}

	got, err := s.DNSKEYAt(context.Background(), "work.example", []string{bad.addr})
	if err == nil {
		t.Fatalf("DNSKEYAt = %+v, want an error", got)
	}

	if !strings.Contains(err.Error(), bad.addr) {
		t.Fatalf("DNSKEYAt error = %v, want a bad signature, naming %s", err, bad.addr)
	}
}

func TestDNSKEYAtReturnsNoKeysWhenTheNameserversServeNone(t *testing.T) {
	a := serveKeys(t, "work.example", nil, signingKey{}, nil)
	b := serveKeys(t, "work.example", nil, signingKey{}, nil)

	s := &System{}

	got, err := s.DNSKEYAt(context.Background(), "work.example", []string{a.addr, b.addr})
	if err != nil {
		t.Fatalf("DNSKEYAt: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("DNSKEYAt = %+v, want no keys", got)
	}
}

func TestDNSKEYAtFailsWithoutNameservers(t *testing.T) {
	s := &System{}

	if got, err := s.DNSKEYAt(context.Background(), "work.example", nil); err == nil {
		t.Fatalf("DNSKEYAt = %+v, want an error", got)
	}
}
