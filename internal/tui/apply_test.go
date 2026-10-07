package tui_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
)

// cachedRecordsOf returns a Zone's cached Records, read back through the Store.
func cachedRecordsOf(t *testing.T, h *harness, name string) []dns.Record {
	t.Helper()

	records, err := h.store.Records(context.Background(), zoneID(t, h.store, name))
	if err != nil {
		t.Fatal(err)
	}

	return records
}

func TestAPartialApplyCachesOnlyTheConfirmedChangesAndKeepsTheFailedOnesPending(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.buildChangeset()

	f.FailChanges(func(c dns.Change) error {
		if c.Kind == dns.ChangeDelete {
			return errors.New("record is locked")
		}

		return nil
	})
	h.press("p", "enter")

	h.assertShows("Applied 2 of 3 changes to work.example; 1 failed and are still pending: record is locked",
		"Changes to work.example", "- @ MX 10 mail.work.example TTL 3600")
	h.assertHides("+ _verify TXT", "~ @ A")

	h.press("esc")
	h.assertShows("- work.example", "10 mail.work.example", "_verify.work.example", "203.0.113.99")

	want := []dns.Record{
		{ProviderID: "fake-1", Type: "TXT", Name: "_verify.work.example", Value: "token-123", TTL: 3600},
		{ProviderID: "rw1", Type: "A", Name: "work.example", Value: "203.0.113.99", TTL: 300},
		liveRecords[1],
	}
	if got := cachedRecordsOf(t, h, "work.example"); !reflect.DeepEqual(got, want) {
		t.Fatalf("cached Records = %+v, want %+v", got, want)
	}

	// The Zone stays validated, and applying again sends only the failed change.
	h.assertMarker("work.example", "✓")
	f.FailChanges(nil)
	h.press("p", "enter")

	calls := applyCalls(f)
	if len(calls) != 2 || !reflect.DeepEqual(calls[1].Changes, []dns.Change{{Kind: dns.ChangeDelete, Before: liveRecords[1]}}) {
		t.Fatalf("Apply calls = %+v", calls)
	}

	h.assertShows("Applied 1 change to work.example.")
	h.assertHides("10 mail.work.example", "pending change")
}

func TestAnAdditionTheProviderReturnsNoRecordForIsNotCachedAndTheZoneNeedsARefresh(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.addRecord("TXT", "_one", "first", "300")
	h.addRecord("TXT", "_two", "second", "300")

	f.WithholdRecords(true)
	h.press("p", "enter")

	h.assertHides("Changes to work.example", "pending change")
	h.assertShows("Applied 2 changes to work.example", "Refresh (r) needed")

	// Without the IDs the Provider gave them, the additions cannot be cached.
	if got := cachedRecordsOf(t, h, "work.example"); !reflect.DeepEqual(got, liveRecords) {
		t.Fatalf("cached Records = %+v, want %+v", got, liveRecords)
	}

	// The Zone is no longer validated: it must be Refreshed before the next edit.
	h.assertMarker("work.example", "✗")
	h.press("i")
	h.assertShows("work.example is a Stale Zone")
	h.assertHides("Add Record")

	h.press("r")
	h.assertMarker("work.example", "✓")
	h.assertShows("_one.work.example", "_two.work.example")
}

func TestTheRecordsPaneShowsTheAppliedChangesAsSoonAsTheApplyReturns(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.buildChangeset()

	h.press("p")
	h.step(h.pressHolding("enter")) // any reload of the cache is held back

	h.assertHides("Changes to work.example", "pending change", "203.0.113.20", "10 mail.work.example")
	h.assertShows("Applied 3 changes to work.example.", "_verify.work.example", "203.0.113.99")
}

// applyDroppingTheZone applies the pending Changeset, but the Zone leaves
// the cache while the Provider applies it, so the cache cannot take the
// changes it confirms.
func (h *harness) applyDroppingTheZone() {
	h.t.Helper()

	h.press("p")
	cmd := h.pressHolding("enter")

	other := cachedZone(h.t, h.store, "work.test")
	if _, err := h.store.SyncAccountZones(context.Background(), other.AccountID, []dns.Zone{other}); err != nil {
		h.t.Fatalf("sync zones: %v", err)
	}

	h.run(cmd)
}

func TestAnApplyTheCacheCannotTakeLeavesTheZoneNeedingARefresh(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.buildChangeset()

	h.applyDroppingTheZone()

	h.assertShows("Applied 3 changes to work.example. Refresh (r) needed: they could not be cached")
	h.assertHides("pending change")

	h.press("i")
	h.assertShows("work.example is a Stale Zone")
	h.assertHides("Add Record")
}

func TestARecordWithoutAProviderIDCannotBeChanged(t *testing.T) {
	f := fake.New().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial},
		dns.Record{Type: "TXT", Name: "work.example", Value: "first", TTL: 300},
		dns.Record{Type: "TXT", Name: "work.example", Value: "second", TTL: 300})
	h := openValidatedShowing(t, f, "second")

	for _, key := range []string{"d", "e"} {
		h.press(key)

		h.assertShows("has no Provider ID")
		h.assertHides("Edit Record")

		if n := strings.Count(h.view(), "- work.example"); n != 0 {
			t.Fatalf("%d rows are marked for deletion, want none:\n%s", n, h.view())
		}
	}
}
