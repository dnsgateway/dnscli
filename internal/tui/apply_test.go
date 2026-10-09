package tui_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dnsgateway/dnsprovider"

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
	h.press("P", "enter")

	h.assertShows("Applied 2 of 3 changes to work.example; 1 failed and are still pending: record is locked",
		"Changes to work.example", "- @ MX 10 mail.work.example TTL 3600")

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
	h.press("P", "enter")

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
	h.press("P", "enter")

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

	h.press("P")
	h.step(h.pressHolding("enter")) // any reload of the cache is held back

	h.assertHides("Changes to work.example", "pending change", "203.0.113.20", "10 mail.work.example")
	h.assertShows("Applied 3 changes to work.example.", "_verify.work.example", "203.0.113.99")
}

// applyDroppingTheZone applies the pending Changeset, but the Zone leaves
// the cache while the Provider applies it, so the cache cannot take the
// changes it confirms.
func (h *harness) applyDroppingTheZone() {
	h.t.Helper()

	h.press("P")
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

func TestAPartialApplyTheCacheCannotTakeSaysBoth(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.buildChangeset()

	f.FailChanges(func(c dns.Change) error {
		if c.Kind == dns.ChangeDelete {
			return errors.New("record is locked")
		}

		return nil
	})
	h.applyDroppingTheZone()

	// The preview stays open on the failed change, with the whole notice.
	h.assertShows("Changes to work.example", "Applied 2 of 3 changes to work.example; 1 failed and",
		"record is locked", "could not be cached", "Refresh (r) needed")
}

func TestDeletingAModifiedRecordDeletesItAsTheChangeWasBuiltFrom(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)

	h.press("e", "tab", "tab")
	h.clearText(len("20"))
	h.typeText("99")
	h.saveForm()
	h.assertShows("~ work.example")

	// The Record changes outside dnscli, and a Refresh caches it.
	changed := liveRecords[0]
	changed.Value = "203.0.113.21"
	f.SetRecords("zw1", changed, liveRecords[1])
	h.press("r")
	h.assertShows("validated this session", "~ work.example", "203.0.113.99")

	h.press("d", "P")

	h.assertShows("- @ A 203.0.113.20 TTL 300")
}

func TestChangingARecordsTypeKeepsTheProxyFlagOnlyWhereItApplies(t *testing.T) {
	proxied := dns.Record{ProviderID: "rw3", Type: "CNAME", Name: "www.work.example", Value: "work.example", TTL: 300,
		Extras: map[string]string{"proxied": "true", "comment": "kept"}}

	cases := []struct {
		typ  string
		want map[string]string
	}{
		{"A", map[string]string{"proxied": "true", "comment": "kept"}},
		{"TXT", map[string]string{"comment": "kept"}},
	}

	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			f := newFake().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial},
				liveRecords[0], liveRecords[1], proxied)
			f.SetProxiableTypes("A", "AAAA", "CNAME")
			h := openValidated(t, f)

			// The CNAME is the third row; step its type to c.typ.
			h.press("j", "j", "e")
			h.assertShows("Edit Record", "‹ CNAME ›")

			key, steps := "right", slices.Index(dnsprovider.EditableTypes, c.typ)-slices.Index(dnsprovider.EditableTypes, "CNAME")
			if steps < 0 {
				key, steps = "left", -steps
			}

			for range steps {
				h.press(key)
			}

			h.press("tab", "tab")
			if c.typ == "A" {
				h.clearText(len("work.example"))
				h.typeText("192.0.2.1")
			}

			h.saveForm()
			h.assertShows("~ www.work.example", "1 pending change")

			h.press("P", "enter")

			calls := applyCalls(f)
			if len(calls) != 1 || len(calls[0].Changes) != 1 {
				t.Fatalf("Apply calls = %+v", calls)
			}

			if got := calls[0].Changes[0].After; got.Type != c.typ || !reflect.DeepEqual(got.Extras, c.want) {
				t.Fatalf("applied Record = %+v, want type %s with extras %v", got, c.typ, c.want)
			}
		})
	}
}

func TestTheRecordFormChecksTheTTLAgainstTheProvidersLimits(t *testing.T) {
	f := refreshZones()
	f.SetTTLRange(60, 3600)
	h := openValidated(t, f)

	for _, ttl := range []string{"30", "7200"} {
		h.addRecord("TXT", "@", "hello", ttl)
		h.assertShows("Add Record", "The TTL must be between 60 and 3600 seconds.")
		h.press("esc")
	}

	// The Provider has no automatic TTL: to it, TTL 1 is one second.
	for _, ttl := range []string{"1", "auto"} {
		h.addRecord("TXT", "@", "hello", ttl)
		h.assertShows("Add Record", "The TTL must be between 60 and 3600 seconds", "no automatic TTL")
		h.assertHides("1 (automatic)", "TTL 1 is automatic")
		h.press("esc")
	}

	h.addRecord("TXT", "@", "hello", "60")
	h.assertHides("Add Record")
	h.assertShows("1 pending change")
}

func TestTheRecordFormTakesAnAutomaticTTLWhereTheProviderHasOne(t *testing.T) {
	f := refreshZones()
	f.SetTTLRange(60, 3600)
	f.SetAutoTTL(true)
	h := openValidated(t, f)

	h.addRecord("TXT", "@", "hello", "30")
	h.assertShows("Add Record", "The TTL must be 1 (automatic) or between 60 and 3600", "TTL 1 is automatic")
	h.press("esc")

	h.addRecord("TXT", "@", "one", "1")
	h.addRecord("TXT", "@", "two", "auto")
	h.assertHides("Add Record")
	h.assertShows("2 pending changes")
}

func TestEditingARecordKeepsItsNamesCase(t *testing.T) {
	mixed := dns.Record{ProviderID: "rw9", Type: "A", Name: "WWW.Work.Example", Value: "192.0.2.1", TTL: 300}
	f := newFake().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial}, mixed)
	h := openValidatedShowing(t, f, "WWW.Work.Example")

	// Submitting the form unchanged changes nothing.
	h.press("e")
	h.assertShows("Edit Record", "WWW")
	h.saveForm()
	h.assertHides("Edit Record", "~ ", "pending change")

	// Nor does typing the same name in another case.
	h.press("e", "tab")
	h.clearText(len("WWW"))
	h.typeText("www")
	h.saveForm()
	h.assertHides("Edit Record", "~ ", "pending change")

	// A real edit keeps the name as the Provider has it.
	h.press("e", "tab", "tab")
	h.clearText(len("1"))
	h.typeText("2")
	h.saveForm()
	h.assertShows("~ WWW.Work.Example", "192.0.2.2")

	h.press("P", "enter")

	calls := applyCalls(f)
	if len(calls) != 1 || calls[0].Changes[0].After.Name != "WWW.Work.Example" {
		t.Fatalf("Apply calls = %+v", calls)
	}
}

func TestARecordWithoutAProviderIDCannotBeChanged(t *testing.T) {
	f := newFake().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial},
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
