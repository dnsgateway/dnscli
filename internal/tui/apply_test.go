package tui_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
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
