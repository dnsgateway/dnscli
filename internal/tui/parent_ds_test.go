// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui_test

import (
	"errors"
	"testing"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// The DS records example.com's parent serves in these tests.
const (
	dsOne = "2371 13 2 1F987CC6583E92DF0890718C42"
	dsTwo = "60485 13 2 D4B7D520E7BB5F0F67674A0CCEB1E3E0614B93C4F9E99B8383F6A1E4469DA50A"
)

// newDSHarness starts a session on the seeded cache with r as its resolver.
func newDSHarness(t *testing.T, r *fakeResolver) (*harness, *store.Store) {
	t.Helper()

	s, _ := openStore(t)
	seedCache(t, s)

	return newSerialCheckHarness(t, s, r), s
}

func TestOpeningAZoneShowsHowManyDSRecordsItsParentServes(t *testing.T) {
	r := &fakeResolver{}
	r.setDS("example.com", dsOne, dsTwo)
	h, _ := newDSHarness(t, r)

	h.press("space")

	h.assertStatusBarShows("DS at parent: yes (2)")

	if got := r.dsQueries(); len(got) != 1 || got[0] != "example.com" {
		t.Fatalf("DS queries = %v, want one for example.com", got)
	}
}

func TestAParentThatServesNoDSSaysNo(t *testing.T) {
	r := &fakeResolver{}
	r.setDS("example.com")
	h, _ := newDSHarness(t, r)

	h.press("space")

	h.assertStatusBarShows("DS at parent: no")
}

func TestAFailedDSCheckShowsUnknownNeverNo(t *testing.T) {
	r := &fakeResolver{}
	r.failDS("example.com", errors.New("i/o timeout"))
	h, _ := newDSHarness(t, r)

	h.press("space")

	h.assertStatusBarShows("DS at parent: unknown")
	h.assertHides("DS at parent: no")
}

func TestADSNotCheckedThisSessionSaysSo(t *testing.T) {
	// Offline: no resolver, so nothing is checked this session.
	s, _ := openStore(t)
	seedCache(t, s)
	h := newHarness(t, s)

	h.press("space")

	h.assertStatusBarShows("DS at parent: not checked this session")
}

func TestTheDSCheckRunsInTheBackgroundOnceTheZoneIsOpened(t *testing.T) {
	r := &fakeResolver{}
	r.setDS("example.com", dsOne)
	h, _ := newDSHarness(t, r)

	// Opening the Zone shows its cached Records, then checks in the background.
	opened := h.step(h.pressHolding("space"))
	h.assertStatusBarShows("DS at parent: checking…")
	h.assertShows("192.0.2.10")

	h.run(opened)
	h.assertStatusBarShows("DS at parent: yes (1)")
}

func TestDSChecksWaitForAFreeSlotAmongTheDelegationLookups(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	r := &fakeResolver{ns: map[string][]string{}}
	r.setDS("example.com", dsOne)
	h, startup := newHarnessHoldingStartup(t, s, tui.Deps{Resolver: r, SerialChecks: 1})

	// The one slot is taken by a Delegation lookup: opening a Zone queues
	// its DS Check behind it.
	lookups := runOnce(startup)
	if got := r.nsQueries(); len(got) != 1 {
		t.Fatalf("NS queries in flight = %v, want 1", got)
	}

	h.press("space")
	h.assertStatusBarShows("DS at parent: checking…")

	if got := r.dsQueries(); len(got) != 0 {
		t.Fatalf("DS queries = %v, want none while the slot is taken", got)
	}

	for _, msg := range lookups {
		h.send(msg)
	}

	if got, ns := r.dsQueries(), r.nsQueries(); len(got) != 1 || len(ns) != 3 {
		t.Fatalf("DS queries = %v, NS queries = %v; want example.com's DS and each zone name's NS asked", got, ns)
	}

	h.assertStatusBarShows("DS at parent: yes (1)")
}

// editDNSSECOff opens work.example's Edit Zone dialog in a session with
// r as its resolver, Unseals, and picks DNSSEC off.
func editDNSSECOff(t *testing.T, r *fakeResolver) *harness {
	t.Helper()

	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	h := newHarnessWith(t, s, tui.Deps{
		Providers:    settingsFake(status, true).registry(),
		UnsealMethod: fastPassphrase(),
		Resolver:     r,
	})

	h.press("e", "enter")
	h.unsealWith(passphrase)
	h.press("right")

	return h
}

func TestTurningDNSSECOffWarnsToRemoveTheDSFirstWhenTheParentServesOne(t *testing.T) {
	r := &fakeResolver{}
	r.setDS("work.example", dsOne)
	h := editDNSSECOff(t, r)

	// The hint warns as soon as off is picked; Save then refuses.
	h.assertShows("Remove the DS record")

	h.press(saveFromDNSSEC...)
	h.assertShows("DNSSEC stays on: remove the DS record for work.example")
	h.assertHides("Turn DNSSEC off for work.example")
}

func TestTurningDNSSECOffDoesNotWarnWhenTheParentServesNoDS(t *testing.T) {
	r := &fakeResolver{}
	r.setDS("work.example")
	h := editDNSSECOff(t, r)

	h.assertHides("Remove the DS record")

	// dnscli never saw a DS go, nor its TTL: it waits a day from now,
	// as long as the largest TLDs' DS TTL, before unsigning.
	h.press(saveFromDNSSEC...)
	h.assertShows("DNSSEC stays on until Oct 8 12:00")
	h.assertHides("Remove the DS record", "Turn DNSSEC off for work.example")
}

func TestRefreshingTheZoneChecksTheDSAtItsParentAgain(t *testing.T) {
	r := &fakeResolver{}
	r.setDS("example.com")
	h, _ := newDSHarness(t, r)

	// Opening the Zone asks to Unseal, for its Refresh: the user declines.
	h.press("space", "esc")
	h.assertStatusBarShows("DS at parent: no")

	// The DS is published at the registrar meanwhile. A Refresh asks for
	// secrets again, but the DS is public: it is checked even sealed.
	r.setDS("example.com", dsOne)
	h.press("right", "r", "esc")

	h.assertStatusBarShows("DS at parent: yes (1)")

	if got := r.dsQueries(); len(got) != 2 {
		t.Fatalf("DS queries = %v, want example.com asked again", got)
	}
}

func TestTurningDNSSECOffWhileTheDSCheckRunsSaysItIsCheckingAndRefuses(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	r := &fakeResolver{ns: map[string][]string{}}
	r.setDS("work.example", dsOne)

	// The one slot is taken by a Delegation lookup that never answers, so
	// the DS Check stays queued while the dialog is open.
	providers := settingsFake(status, true).registry()
	h, _ := newHarnessHoldingStartup(t, s, tui.Deps{
		Providers:    providers,
		UnsealMethod: fastPassphrase(),
		Resolver:     r,
		SerialChecks: 1,
	})
	h.providers = providers

	h.press("e", "enter")
	h.unsealWith(passphrase)
	h.press("right")

	h.assertShows("is checking whether the parent")
	h.assertHides("could not tell")

	// Save refuses until the check answers.
	h.press(saveFromDNSSEC...)
	h.assertShows("DNSSEC stays on: dnscli is checking whether the parent")
	h.assertHides("Turn DNSSEC off for work.example", "could not tell")
}
