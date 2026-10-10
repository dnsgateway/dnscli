package tui_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// The key and its SHA-256 DS from RFC 4509, section 2.3, for
// dskey.example.com: a known-good pair to match the parent's DS against.
const (
	rfcZone   = "dskey.example.com"
	rfcDNSKEY = "256 3 5 AQOeiiR0GOMYkDshWoSKz9XzfwJr1AYtsmx3TGkJaNXVbfi/2pHm822aJ5iI9BMzNXxeYCmZDRD99WYwYqUSdjMmmAphXdvxegXd/M5+X7OrzKBaMbCVdFLUUh6DhweJBjEVv5f2wwjM9XzcnOf+EPbtG9DMBmADjFDc2w/rljwvFw=="
	rfcDS     = "60485 5 2 D4B7D520E7BB5F0F67674A0CCEB1E3E0614B93C4F9E99B8383F6A1E4469DA50A"
)

// rfcKey is the RFC 4509 key as the resolver gives it.
var rfcKey = dns.SigningKey{DNSKEY: rfcDNSKEY, DS: []string{rfcDS}}

// newSignedZoneHarness starts an unsealed session with cloudflare.work,
// which hosts dskey.example.com, signed, as the Live Zone on cruzDee, with
// r as its resolver, and opens the Zone.
func newSignedZoneHarness(t *testing.T, r *fakeResolver) *harness {
	t.Helper()

	return newKeysHarness(t, r, dnsprovider.On)
}

// newKeysHarness is newSignedZoneHarness with the Zone's DNSSEC as its
// Provider reports it.
func newKeysHarness(t *testing.T, r *fakeResolver, dnssec dnsprovider.Setting) *harness {
	t.Helper()

	f := newFake().AddZone(dns.Zone{Name: rfcZone, ProviderID: "zd1", Nameservers: cruzDee})
	f.SetZoneStatus("zd1", dnsprovider.ZoneStatus{DNSSEC: dnssec})

	if r.ns == nil {
		r.ns = map[string][]string{}
	}

	r.ns[rfcZone] = cruzDee

	s, _ := openStore(t)
	h := newHarnessWith(t, s, tui.Deps{Providers: f.registry(), UnsealMethod: fastPassphrase(), Resolver: r})
	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")
	h.assertShows(rfcZone, "unsealed")

	h.press("space")

	return h
}

func TestAParentDSThatMatchesAKeySigningKeyIsDNSSECOn(t *testing.T) {
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone, rfcDS)
	h := newSignedZoneHarness(t, r)

	h.assertStatusBarShows("DNSSEC: On")
	h.assertBadges(map[string]string{"DNSSEC: On": green})

	if q := r.keysQueries(); len(q) == 0 || q[0].zone != rfcZone || !slices.Equal(q[0].servers, cruzDee) {
		t.Fatalf("DNSKEY queries = %+v, want %s asked at its nameservers %v", q, rfcZone, cruzDee)
	}
}

func TestAParentDSThatMatchesNoKeySaysItDoesNotMatch(t *testing.T) {
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone, dsOne)
	h := newSignedZoneHarness(t, r)

	h.assertStatusBarShows("DNSSEC: DS does not match")
	h.assertBadges(map[string]string{"DNSSEC: DS does not match": red})
	h.assertHides("DNSSEC: On")
}

// ecKey is an ECDSA P-256 key-signing key short enough to show on one line.
var ecKey = dns.SigningKey{
	DNSKEY: "257 3 13 mdsswUyr3DPW132mOi8V9xESWE8jTo0dxCjjnopKl+GqJxpVXckHAeF+KkxLbxILfDLUT0rAK9iUzy1L53eKGQ==",
	DS:     []string{"2371 13 2 1F987CC6583E92DF0890718C42"},
}

func TestWithoutADSAtTheParentTheKeysAreShownToCopy(t *testing.T) {
	r := &fakeResolver{}
	r.setKeys(rfcZone, ecKey)
	r.setDS(rfcZone)
	h := newSignedZoneHarness(t, r)

	h.assertStatusBarShows("DNSSEC: publish the DS")
	h.assertHides(ecKey.DNSKEY)

	h.press("s")
	h.assertShows("DNSKEY", ecKey.DNSKEY, "DS", ecKey.DS[0])

	h.press("esc")
	h.assertHides(ecKey.DNSKEY)
}

func TestKeysTheNameserversDoNotAgreeOnAreWaitedFor(t *testing.T) {
	r := &fakeResolver{}
	r.failKeys(rfcZone, fmt.Errorf("dee.ns.cloudflare.com: %w", dns.ErrNameserversDisagree))
	r.setDS(rfcZone)
	h := newSignedZoneHarness(t, r)

	h.assertStatusBarShows("DNSSEC: waiting for nameservers")

	h.press("s")
	h.assertHides("DNSKEY")
}

func TestASignedZoneWhoseNameserversServeNoKeysYetIsWaitedFor(t *testing.T) {
	r := &fakeResolver{}
	r.setKeys(rfcZone)
	r.setDS(rfcZone)
	h := newSignedZoneHarness(t, r)

	h.assertStatusBarShows("DNSSEC: waiting for nameservers")
}

func TestAFailedKeyLookupShowsUnknown(t *testing.T) {
	r := &fakeResolver{}
	r.failKeys(rfcZone, errors.New("i/o timeout"))
	r.setDS(rfcZone, rfcDS)
	h := newSignedZoneHarness(t, r)

	h.assertStatusBarShows("DNSSEC: unknown")
	h.assertHides("DNSSEC: On", "does not match")
}

func TestAFailedDSCheckWithKeysShowsUnknown(t *testing.T) {
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.failDS(rfcZone, errors.New("i/o timeout"))
	h := newSignedZoneHarness(t, r)

	h.assertStatusBarShows("DNSSEC: unknown")
	h.assertHides("DNSSEC: On", "does not match", "publish the DS")
}

func TestAParentDSForAZoneThatServesNoKeysSaysItDoesNotMatch(t *testing.T) {
	for _, dnssec := range []dnsprovider.Setting{dnsprovider.Off, dnsprovider.Unknown, dnsprovider.On} {
		t.Run(dnssec.String(), func(t *testing.T) {
			r := &fakeResolver{}
			r.setKeys(rfcZone)
			r.setDS(rfcZone, rfcDS)
			h := newKeysHarness(t, r, dnssec)

			h.assertStatusBarShows("DNSSEC: DS does not match")
			h.assertBadges(map[string]string{"DNSSEC: DS does not match": red})
		})
	}
}
