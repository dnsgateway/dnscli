package tui_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// rfcModuleKey is the RFC 6605 key as a registrar adapter takes it.
var rfcModuleKey = dnsprovider.SigningKey{DNSKEY: rfcDNSKEY, KeyTag: 55648, DS: []string{rfcDS}}

// setDSCalls are the DS writes the fake Provider was asked to make.
func setDSCalls(f *fakeProvider) []providertest.Call {
	var out []providertest.Call

	for _, c := range f.Calls() {
		if c.Op == providertest.OpSetDelegationSigners {
			out = append(out, c)
		}
	}

	return out
}

// assertPublishedOnce checks that f was asked once to replace the DS of
// its Zone zoneID with the RFC 6605 key.
func assertPublishedOnce(t *testing.T, f *fakeProvider, zoneID string) {
	t.Helper()

	calls := setDSCalls(f)
	if len(calls) != 1 || calls[0].Zone.ID != zoneID || !reflect.DeepEqual(calls[0].Keys, []dnsprovider.SigningKey{rfcModuleKey}) {
		t.Fatalf("SetDelegationSigners calls = %+v, want one setting %+v for %s", calls, rfcModuleKey, zoneID)
	}
}

// newPublishHarness is newTurnOnHarness with example.net signed and its
// keys served, the RFC 6605 key unless keys are given, but no DS at the
// parent, once its wait has passed: the DS is to be published.
func newPublishHarness(t *testing.T, f *fakeProvider, r *fakeResolver, c *testClock, keys ...dns.SigningKey) *harness {
	t.Helper()

	if len(keys) == 0 {
		keys = []dns.SigningKey{rfcKey}
	}

	r.setKeys(rfcZone, keys...)
	r.setDS(rfcZone)

	h, _ := newTurnOnHarness(t, f, r, c)
	h.passTime(time.Hour)
	h.assertStatusBarShows("DNSSEC: publish the DS")

	return h
}

func TestTheDSIsPublishedAtTheLiveZonesOwnAccountWhenItRegistersTheDomain(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1")

	h := newPublishHarness(t, f, r, c)
	h.assertStatusBarShows("DNSSEC: publish the DS (S publishes it, s shows it)")

	h.press("S")
	h.assertShows("Publish the DS of example.net at cloudflare.work?", rfcDS)

	if calls := setDSCalls(f); len(calls) != 0 {
		t.Fatalf("SetDelegationSigners before confirming: %+v", calls)
	}

	h.press("enter")
	assertPublishedOnce(t, f, "zd1")

	// Published, it is On only once the parent zone serves the DS.
	h.assertStatusBarShows("DNSSEC: DS published, waiting for the parent zone")
	h.assertHides("DNSSEC: On")

	r.setDS(rfcZone, rfcDS)
	h.passTime(time.Minute)
	h.assertStatusBarShows("DNSSEC: On")
}

// registrarFake is a Provider registering example.net as zp1, which it
// serves no DNS for: a registration-only Zone, its DS records as given.
func registrarFake(ds ...dnsprovider.SigningKey) *fakeProvider {
	g := newFake().AddZone(dns.Zone{Name: rfcZone, ProviderID: "zp1"})
	g.SetRegistration("zp1", ds...)
	g.Fail(providertest.OpRecords, dnsprovider.ErrNotServed)

	return g
}

// newRegistrarHarness starts an unsealed session with cloudflare.work at
// f, which hosts example.net as the Live Zone on cruzDee, signed with its
// keys served but no DS at the parent, and porkbun.home at g; it Refreshes
// porkbun.home's example.net, then opens cloudflare.work's once its wait
// has passed: the DS is to be published.
func newRegistrarHarness(t *testing.T, f, g *fakeProvider, r *fakeResolver, c *testClock) *harness {
	t.Helper()

	r.ns = map[string][]string{rfcZone: cruzDee}
	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone)

	s, _ := openStore(t)
	h := newHarnessAt(t, s, tui.Deps{
		Providers:    dnsprovider.Registry{"cloudflare": f.Factory(), "porkbun": g.Factory()},
		UnsealMethod: fastPassphrase(),
		Resolver:     r,
	}, c)
	// Wide enough for the Domains rows to name their Accounts.
	h.send(tea.WindowSizeMsg{Width: 160, Height: 30})
	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")
	h.assertShows("unsealed")

	h.fillAccountFields("porkbun", "home", token)
	h.assertShows(rfcZone + " · porkbun.home")

	h.openZone(rfcZone + " · porkbun.home")
	h.openZone(rfcZone + " · cloudflare.work")
	h.assertShows("validated this session")

	h.passTime(time.Hour)
	h.assertStatusBarShows("DNSSEC: publish the DS")

	return h
}

func TestTheDSIsPublishedAtARegistrationOnlyZoneInAnotherAccount(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)
	g := registrarFake()

	h := newRegistrarHarness(t, f, g, r, c)

	h.press("S")
	h.assertShows("Publish the DS of example.net at porkbun.home?", rfcDS)

	h.press("enter")
	assertPublishedOnce(t, g, "zp1")
	h.assertStatusBarShows("DNSSEC: DS published, waiting for the parent zone")
}

// openZone opens the Zone whose Domains row shows row, from the Domains
// pane.
func (h *harness) openZone(row string) {
	h.t.Helper()

	for range 4 {
		if strings.Contains(h.view(), "┏━ Domains") {
			break
		}

		h.press("tab")
	}

	for range 10 {
		h.press("k")
	}

	for range 10 {
		if h.highlighted(row) {
			h.press("space")

			return
		}

		h.press("j")
	}

	h.t.Fatalf("no Domains row %q:\n%s", row, h.view())
}

func TestARegistrarAccountThatDoesNotRegisterTheDomainIsSkipped(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}

	// cloudflare.work's Provider registers domains, but not example.net.
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("elsewhere")

	g := registrarFake()

	h := newRegistrarHarness(t, f, g, r, c)

	h.press("S")
	h.assertShows("Publish the DS of example.net at porkbun.home?")
	h.assertHides("cloudflare.work?")

	if f.count(providertest.OpDelegationSigners) == 0 {
		t.Fatal("cloudflare.work was not asked whether it registers example.net")
	}

	h.press("enter")
	assertPublishedOnce(t, g, "zp1")

	if calls := setDSCalls(f); len(calls) != 0 {
		t.Fatalf("SetDelegationSigners at the Account that does not register it: %+v", calls)
	}
}

func TestTwoAccountsThatRegisterTheDomainAreOfferedToPickFromEachTime(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1")

	g := registrarFake()

	h := newRegistrarHarness(t, f, g, r, c)

	// The Live Zone's own Account is listed first, then the
	// registration-only Zone's.
	h.press("S")
	h.assertShows("Publish the DS of example.net at")
	h.assertRowsInOrder("cloudflare.work", "porkbun.home (registration only)")

	h.press("j", "enter")
	h.assertShows("Publish the DS of example.net at porkbun.home?")

	// Canceled, the pick is not kept: the next time asks again.
	h.press("esc")
	h.press("S")
	h.assertShows("Publish the DS of example.net at")
	h.assertRowsInOrder("cloudflare.work", "porkbun.home (registration only)")

	h.press("j", "enter", "enter")
	assertPublishedOnce(t, g, "zp1")

	if calls := setDSCalls(f); len(calls) != 0 {
		t.Fatalf("SetDelegationSigners at the Account not picked: %+v", calls)
	}
}

// assertRowsInOrder checks that each text shows, on lines of their own,
// in the order given.
func (h *harness) assertRowsInOrder(texts ...string) {
	h.t.Helper()

	v := h.view()
	at := 0

	for _, s := range texts {
		i := strings.Index(v[at:], s)
		if i < 0 {
			h.t.Fatalf("%q does not show after %q:\n%s", s, texts, v)
		}

		at += i + len(s)
	}
}

func TestWithoutAnAccountThatRegistersTheDomainTheKeysAreShownToCopy(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)

	h := newPublishHarness(t, f, r, c)

	h.press("S")
	h.assertShows("DNSSEC keys for example.net", rfcDS, "No Account in dnscli publishes the DS of example.net")
	h.assertHides("Publish the DS of")
}

func TestCancellingThePublishConfirmationWritesNothing(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1")

	h := newPublishHarness(t, f, r, c)

	h.press("S")
	h.assertShows("Publish the DS of example.net at cloudflare.work?")

	h.press("esc")
	h.assertHides("Publish the DS of")
	h.assertStatusBarShows("DNSSEC: publish the DS (S publishes it, s shows it)")

	if calls := setDSCalls(f); len(calls) != 0 {
		t.Fatalf("SetDelegationSigners once canceled: %+v", calls)
	}
}

func TestARegistryThatRefusesTheKeySaysSoClearly(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1")
	f.Fail(providertest.OpSetDelegationSigners, fmt.Errorf("cloudflare: algorithm 13 is not supported for .net: %w", dnsprovider.ErrUnsupported))

	h := newPublishHarness(t, f, r, c)

	h.press("S", "enter")
	h.assertShows("The registry of example.net refuses", "algorithm 13 is not supported", "not a dnscli error")
	h.assertStatusBarShows("DNSSEC: publish the DS")
	h.assertHides("DS published")
}

func TestASealedSessionIsShownTheKeysToCopyAndAsksNoRegistrar(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	r.setKeys(rfcZone, rfcKey)
	r.setDS(rfcZone)
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1")

	_, s := newTurnOnHarness(t, f, r, c)
	seenSigned(t, s, rfcZone, testNow.Add(-2*time.Hour))

	h := restart(t, s, f, r, c)
	h.assertStatusBarShows("DNSSEC: publish the DS (s shows it)")
	h.assertHides("S publishes it")

	h.press("S")
	h.assertShows("DNSSEC keys for example.net", rfcDS)
	h.assertHides("Publish the DS of")

	if n := f.count(providertest.OpDelegationSigners); n != 0 {
		t.Fatalf("DelegationSigners asked %d times in a sealed session", n)
	}
}

func TestThePublishConfirmationShowsEveryKeyAndDSItSends(t *testing.T) {
	c := &testClock{now: testNow}
	r := &fakeResolver{}
	f := turnOnFake(dnsprovider.On)
	f.SetRegistration("zd1")

	// The RFC 6605 key with a second DS, and a key with none: each is
	// sent whole, so each shows whole.
	sha384 := "55648 13 4 D4FCD1A5B5E0B1E6E58A1CF1CB2B0A0B0C0D0E0F101112131415161718191A1B1C1D1E1F202122232425262728292A2B2C2D2E2F"
	bare := dns.SigningKey{DNSKEY: ecKey.DNSKEY, KeyTag: ecKey.KeyTag}

	h := newPublishHarness(t, f, r, c, dns.SigningKey{DNSKEY: rfcDNSKEY, KeyTag: 55648, DS: []string{rfcDS, sha384}}, bare)
	h.send(tea.WindowSizeMsg{Width: 200, Height: 40})

	h.press("S")
	h.assertShows("Publish the DS of example.net at cloudflare.work?", rfcDNSKEY, rfcDS, sha384, ecKey.DNSKEY)

	h.press("enter")

	calls := setDSCalls(f)
	if len(calls) != 1 || len(calls[0].Keys) != 2 || len(calls[0].Keys[0].DS) != 2 || calls[0].Keys[1].DNSKEY != ecKey.DNSKEY {
		t.Fatalf("SetDelegationSigners calls = %+v, want both keys, the first with both DS", calls)
	}
}
