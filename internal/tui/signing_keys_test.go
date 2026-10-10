package tui_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// The ECDSA P-256 key-signing key (flag 257) and its SHA-256 DS from
// RFC 6605, section 6.1, for example.net: a known-good pair to match the
// parent's DS against.
const (
	rfcZone   = "example.net"
	rfcDNSKEY = "257 3 13 GojIhhXUN/u4v54ZQqGSnyhWJwaubCvTmeexv7bR6edbkrSqQpF64cYbcB7wNcP+e+MAnLr+Wi9xMWyQLc8NAA=="
	rfcDS     = "55648 13 2 B4C8C1FE2E7477127B27115656AD6256F424625BF5C1E2770CE6D6E37DF61D17"
)

// rfcKey is the RFC 6605 key as the resolver gives it.
var rfcKey = dns.SigningKey{DNSKEY: rfcDNSKEY, KeyTag: 55648, DS: []string{rfcDS}}

// newSignedZoneHarness starts an unsealed session with cloudflare.work,
// which hosts example.net, signed, as the Live Zone on cruzDee, with
// r as its resolver, and opens the Zone.
func newSignedZoneHarness(t *testing.T, r *fakeResolver) *harness {
	t.Helper()

	return newKeysHarness(t, r, dnsprovider.On)
}

// newKeysHarness is newSignedZoneHarness with the Zone's DNSSEC as its
// Provider reports it.
func newKeysHarness(t *testing.T, r *fakeResolver, dnssec dnsprovider.Setting) *harness {
	t.Helper()

	return newKeysHarnessWith(t, newKeysFake(dnssec), r)
}

// newKeysFake is a Provider hosting example.net as zd1, its DNSSEC as
// reported.
func newKeysFake(dnssec dnsprovider.Setting) *fakeProvider {
	f := newFake().AddZone(dns.Zone{Name: rfcZone, ProviderID: "zd1", Nameservers: cruzDee})
	f.SetZoneStatus("zd1", dnsprovider.ZoneStatus{DNSSEC: dnssec})

	return f
}

// newKeysHarnessWith is newKeysHarness with f as the Zone's Provider.
func newKeysHarnessWith(t *testing.T, f *fakeProvider, r *fakeResolver) *harness {
	t.Helper()

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

// ecKey is an ECDSA P-256 key-signing key short enough to show on one
// line, with a made-up DS to show: never matched against a parent's DS.
var ecKey = dns.SigningKey{
	DNSKEY: "257 3 13 mdsswUyr3DPW132mOi8V9xESWE8jTo0dxCjjnopKl+GqJxpVXckHAeF+KkxLbxILfDLUT0rAK9iUzy1L53eKGQ==",
	KeyTag: 2371,
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

	// The keys say why they are waited for, naming the nameserver.
	h.assertHides("dee.ns.cloudflare.com")
	h.press("s")
	h.assertHides("DNSKEY")
	h.assertShows("Could not read the keys", "dee.ns.cloudflare.com:", "different key-signing keys")
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
	r.failKeys(rfcZone, errors.New("cruz.ns.cloudflare.com: i/o timeout"))
	r.setDS(rfcZone, rfcDS)
	h := newSignedZoneHarness(t, r)

	h.assertStatusBarShows("DNSSEC: unknown")
	h.assertHides("DNSSEC: On", "does not match")

	// The keys say why they are unknown, naming the nameserver.
	h.assertHides("cruz.ns.cloudflare.com")
	h.press("s")
	h.assertShows("Could not read the keys", "cruz.ns.cloudflare.com: i/o timeout")
}

func TestAFailedDSCheckWithKeysShowsUnknown(t *testing.T) {
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.failDS(rfcZone, errors.New("i/o timeout"))
	h := newSignedZoneHarness(t, r)

	h.assertStatusBarShows("DNSSEC: unknown")
	h.assertHides("DNSSEC: On", "does not match", "publish the DS")
}

func TestTheKeysAreLookedUpOnceTheDelegationNamesTheNameservers(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	// example.com has no nameservers cached: its Delegation names them.
	r := &fakeResolver{ns: map[string][]string{"example.com": cruzDee}}
	r.setKeys("example.com", ecKey)
	r.setDS("example.com")
	h, startup := newHarnessHoldingStartup(t, s, tui.Deps{Resolver: r})

	h.press("space")

	if q := r.keysQueries(); len(q) != 0 {
		t.Fatalf("DNSKEY queries = %+v, want none before the nameservers are known", q)
	}

	h.run(startup)

	if q := r.keysQueries(); len(q) != 1 || q[0].zone != "example.com" || !slices.Equal(q[0].servers, cruzDee) {
		t.Fatalf("DNSKEY queries = %+v, want example.com asked at its delegated nameservers %v", q, cruzDee)
	}

	h.assertStatusBarShows("DNSSEC: publish the DS")
}

func TestARefreshThatTurnsDNSSECOnLooksUpTheKeysAgain(t *testing.T) {
	r := &fakeResolver{}
	r.setKeys(rfcZone)
	r.setDS(rfcZone)

	f := newFake().AddZone(dns.Zone{Name: rfcZone, ProviderID: "zd1", Nameservers: cruzDee})
	f.SetZoneStatus("zd1", dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On})

	r.ns = map[string][]string{rfcZone: cruzDee}

	s, _ := openStore(t)
	h := newHarnessWith(t, s, tui.Deps{Providers: f.registry(), UnsealMethod: fastPassphrase(), Resolver: r})
	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")
	h.assertShows(rfcZone, "unsealed")

	// The keys are read before the Refresh says DNSSEC is on, while the
	// nameservers serve none yet; they serve the key by the time it does.
	msgs := runOnce(h.step(h.pressHolding("space")))
	if q := r.keysQueries(); len(q) != 1 {
		t.Fatalf("DNSKEY queries = %+v, want one sent on opening the Zone", q)
	}

	r.setKeys(rfcZone, ecKey)

	for _, msg := range msgs {
		h.send(msg)
	}

	h.assertStatusBarShows("DNSSEC: publish the DS")
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

// rfcProviderKey is the RFC 6605 key as a Provider's API returns it.
var rfcProviderKey = dnsprovider.SigningKey{DNSKEY: rfcDNSKEY, KeyTag: 55648, DS: []string{rfcDS}}

func TestTheKeysAProviderReturnsAreUsedWithoutAskingTheNameservers(t *testing.T) {
	f := newKeysFake(dnsprovider.On)
	f.SetSigningKeys("zd1", rfcProviderKey)

	// The nameservers serve no key yet: the Provider's API is ahead.
	r := &fakeResolver{}
	r.setKeys(rfcZone)
	r.setDS(rfcZone, rfcDS)
	h := newKeysHarnessWith(t, f, r)

	h.assertStatusBarShows("DNSSEC: On")

	if q := r.keysQueries(); len(q) != 0 {
		t.Fatalf("DNSKEY queries = %+v, want none when the Provider returns the keys", q)
	}

	if n := f.count(providertest.OpSigningKeys); n == 0 {
		t.Fatal("the Provider was not asked for the signing keys")
	}
}
