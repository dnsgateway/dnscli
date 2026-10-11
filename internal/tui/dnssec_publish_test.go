package tui_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"
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
// keys served, but no DS at the parent, once its wait has passed: the DS
// is to be published.
func newPublishHarness(t *testing.T, f *fakeProvider, r *fakeResolver, c *testClock) *harness {
	t.Helper()

	r.setKeys(rfcZone, rfcKey)
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
