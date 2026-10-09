package tui_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// applyHoldingThePolls opens work.example, at a Provider that reports no
// serial, with the serial its nameservers answer cached, builds a
// Changeset and applies it. It returns the first of the background polls
// of the nameservers for the new serial, held back.
func applyHoldingThePolls(t *testing.T, r *fakeResolver, f *fakeProvider) (*harness, tea.Cmd) {
	t.Helper()

	h, _, _ := newRefreshHarnessWith(t, f, tui.Deps{Resolver: r})
	h.press("space", "tab")
	h.assertShows("validated this session")
	h.buildChangeset()

	h.press("P")

	return h, h.step(h.pressHolding("enter"))
}

func assertCachedSerial(t *testing.T, h *harness, want uint32) {
	t.Helper()

	if z := cachedZone(t, h.store, "work.example"); z.Serial != want {
		t.Fatalf("cached serial = %d, want %d", z.Serial, want)
	}
}

func assertWaits(t *testing.T, h *harness, want ...time.Duration) {
	t.Helper()

	if !reflect.DeepEqual(h.waits, want) {
		t.Fatalf("waits = %v, want %v", h.waits, want)
	}
}

func TestAnApplyPollsTheNameserversForTheNewSerialInTheBackground(t *testing.T) {
	r := &fakeResolver{soaAt: map[string]uint32{"work.example": 2026100700}}
	h, poll := applyHoldingThePolls(t, r, serialZones(zoneNS))
	asked := len(r.soaAtQueries())

	// Right after the Apply the nameservers are not asked: they have not
	// taken the changes yet.
	h.assertShows("Applied 3 changes to work.example.")
	h.press("esc") // past the notice, to the Zone's status
	h.assertShows("serial updating after Apply")
	h.assertHides("possibly stale")
	assertWaits(t, h)

	// Five seconds later, they still answer with the old serial.
	poll = h.step(poll)
	h.assertShows("serial updating after Apply")
	assertCachedSerial(t, h, 2026100700)

	if n := len(r.soaAtQueries()) - asked; n != 1 {
		t.Fatalf("SOA queries since the Apply = %d, want 1", n)
	}

	// Fifteen seconds after the Apply, they have the new one.
	r.setSOAAt("work.example", 2026100701)
	h.run(h.step(poll))

	assertCachedSerial(t, h, 2026100701)
	h.assertHides("serial updating after Apply")
	assertWaits(t, h, 5*time.Second, 10*time.Second)
}

func TestAnApplyStopsPollingForTheSerialAfterThreeTries(t *testing.T) {
	r := &fakeResolver{soaAt: map[string]uint32{"work.example": 2026100700}}
	h, poll := applyHoldingThePolls(t, r, serialZones(zoneNS))

	h.run(poll)
	h.press("esc") // past the notice, to the Zone's status

	assertCachedSerial(t, h, 2026100700)
	h.assertShows("validated this session")
	h.assertHides("serial updating after Apply")
	assertWaits(t, h, 5*time.Second, 10*time.Second, 30*time.Second)
}

func TestWithNoSerialCachedAnApplyStoresASerialThatChangesBetweenPolls(t *testing.T) {
	r := &fakeResolver{soaAt: map[string]uint32{"work.example": 0}}
	h, poll := applyHoldingThePolls(t, r, serialZones(zoneNS))

	h.press("esc") // past the notice, to the Zone's status

	// The first answer may be from before or after the Apply.
	r.setSOAAt("work.example", 2026100701)
	poll = h.step(poll)
	assertCachedSerial(t, h, 0)

	poll = h.step(poll)
	assertCachedSerial(t, h, 0)
	h.assertShows("serial updating after Apply")

	// A later answer that differs is after the Apply.
	r.setSOAAt("work.example", 2026100702)
	h.run(h.step(poll))

	assertCachedSerial(t, h, 2026100702)
	h.assertHides("serial updating after Apply")
}

func TestWithNoSerialCachedAnApplyThatChangedNothingStoresTheFirstAnswer(t *testing.T) {
	r := &fakeResolver{soaAt: map[string]uint32{"work.example": 0}}
	f := serialZones(zoneNS)
	f.FailChanges(func(dns.Change) error { return errors.New("record is locked") })
	h, poll := applyHoldingThePolls(t, r, f)
	h.assertShows("Applied 0 of 3 changes")

	r.setSOAAt("work.example", 2026100701)
	h.run(h.step(poll))

	assertCachedSerial(t, h, 2026100701)
	assertWaits(t, h, 5*time.Second)
}

func TestAnApplyThatChangedNothingDoesNotPollForANewerSerial(t *testing.T) {
	r := &fakeResolver{soaAt: map[string]uint32{"work.example": 2026100700}}
	f := serialZones(zoneNS)
	f.FailChanges(func(dns.Change) error { return errors.New("record is locked") })
	h, poll := applyHoldingThePolls(t, r, f)
	asked := len(r.soaAtQueries())

	h.run(poll)

	h.assertHides("serial updating after Apply")
	assertWaits(t, h)

	if n := len(r.soaAtQueries()) - asked; n != 0 {
		t.Fatalf("SOA queries since the Apply = %d, want none", n)
	}
}

func TestQuittingStopsPollingForTheSerial(t *testing.T) {
	r := &fakeResolver{soaAt: map[string]uint32{"work.example": 2026100700}}
	h, poll := applyHoldingThePolls(t, r, serialZones(zoneNS))
	asked := len(r.soaAtQueries())

	h.press("q")
	h.run(poll)

	if n := len(r.soaAtQueries()) - asked; n != 0 {
		t.Fatalf("SOA queries after quitting = %d, want none", n)
	}
}

func TestAnApplyWithoutABatcherWritesEachChangeAndPollsForTheSerial(t *testing.T) {
	f := serialZones(zoneNS)
	f.WithholdSerial(false) // Records report the serial; CRUD writes cannot
	f.SetBatcher(false)

	r := &fakeResolver{soaAt: map[string]uint32{"work.example": liveSerial}}
	h, poll := applyHoldingThePolls(t, r, f)

	h.assertShows("Applied 3 changes to work.example.")

	var ops []providertest.Op

	for _, c := range f.Calls() {
		switch c.Op {
		case providertest.OpApply:
			t.Fatal("the Changeset went to a Batcher")
		case providertest.OpCreateRecord, providertest.OpUpdateRecord, providertest.OpDeleteRecord:
			ops = append(ops, c.Op)
		}
	}

	if want := []providertest.Op{providertest.OpDeleteRecord, providertest.OpUpdateRecord, providertest.OpCreateRecord}; !reflect.DeepEqual(ops, want) {
		t.Fatalf("writes = %v, want %v", ops, want)
	}

	want := []dns.Record{
		{ProviderID: "fake-1", Type: "TXT", Name: "_verify.work.example", Value: "token-123", TTL: 3600},
		{ProviderID: "rw1", Type: "A", Name: "work.example", Value: "203.0.113.99", TTL: 300},
	}
	if got := cachedRecordsOf(t, h, "work.example"); !reflect.DeepEqual(got, want) {
		t.Fatalf("cached Records = %+v, want %+v", got, want)
	}

	// No serial came back with the writes: the nameservers are polled.
	h.press("esc")
	h.assertShows("serial updating after Apply")
	assertCachedSerial(t, h, liveSerial)

	r.setSOAAt("work.example", liveSerial+1)
	h.run(h.step(poll))

	assertCachedSerial(t, h, liveSerial+1)
	h.assertHides("serial updating after Apply")
}
