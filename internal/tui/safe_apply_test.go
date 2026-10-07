package tui_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
)

func TestApplyReChecksTheAffectedRecordsBeforeSending(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.buildChangeset()

	h.press("p", "enter")

	calls := f.Calls()
	last := len(calls) - 1

	if last < 1 || calls[last].Op != fake.OpApply || calls[last-1].Op != fake.OpRecords || calls[last-1].Zone.ProviderID != "zw1" {
		t.Fatalf("the Provider was not asked for the Zone's Records right before Apply: %+v", calls)
	}

	h.assertShows("Applied 3 changes to work.example.")
}

func TestAnApplyOfOnlyAdditionsDoesNotReCheckTheZone(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.addRecord("TXT", "_verify", "token-123", "3600")
	before := recordsCalls(f, "zw1")

	h.press("p", "enter")

	h.assertShows("Applied 1 change to work.example.")

	if n := recordsCalls(f, "zw1") - before; n != 0 {
		t.Fatalf("Records calls = %d, want none: an addition is built from no Record", n)
	}
}

func TestAnApplyWhoseRecordsChangedAtTheProviderIsNotSentAndRefreshesTheZone(t *testing.T) {
	changed := liveRecords[0]
	changed.Value = "203.0.113.21"

	cases := map[string]struct {
		live []dns.Record
		want []string
	}{
		"a modified Record changed": {
			live: []dns.Record{changed, liveRecords[1]},
			want: []string{"@ A 203.0.113.20 TTL 300 is now @ A 203.0.113.21 TTL 300"},
		},
		"a deleted Record is gone": {
			live: []dns.Record{liveRecords[0]},
			want: []string{"@ MX 10 mail.work.example TTL 3600 no longer exists"},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := refreshZones()
			h := openValidated(t, f)
			h.buildChangeset()

			// Someone changes the Zone outside dnscli.
			f.SetRecords("zw1", c.live...)
			before := recordsCalls(f, "zw1")

			h.press("p", "enter")

			if calls := applyCalls(f); len(calls) != 0 {
				t.Fatalf("a conflicting Changeset was sent: %+v", calls)
			}

			h.assertShows(append([]string{"Nothing was applied to work.example", "changed at the Provider"}, c.want...)...)

			// The re-check and then a Refresh of the Zone.
			if n := recordsCalls(f, "zw1") - before; n != 2 {
				t.Fatalf("Records calls = %d, want the re-check and a Refresh", n)
			}

			if got := cachedRecordsOf(t, h, "work.example"); !reflect.DeepEqual(got, c.live) {
				t.Fatalf("cached Records = %+v, want the Refreshed %+v", got, c.live)
			}

			// The changes stay pending for the user to review.
			h.press("esc")
			h.assertMarker("work.example", "✓")
			h.assertShows("+ _verify.work.example", "~ work.example", "- work.example")
		})
	}
}

func TestAPartialApplyShowsTheOutcomeOfEachChange(t *testing.T) {
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

	h.assertShows("The Provider applied 2 of 3 changes:",
		"✓ + _verify TXT token-123 TTL 3600",
		"✓ ~ @ A 203.0.113.20 TTL 300 → @ A 203.0.113.99 TTL 300",
		"✗ - @ MX 10 mail.work.example TTL 3600",
		"    record is locked",
		"Applying sends 1 pending change to the Provider:")

	// The outcomes belong to that Apply: reopening the preview drops them.
	h.press("esc", "p")
	h.assertShows("Applying sends 1 pending change")
	h.assertHides("The Provider applied", "✓ +", "✗ -")
}

func TestAnApplyStoresTheSerialTheProviderReports(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.buildChangeset()

	h.press("p", "enter")

	// The fake Provider bumps the serial once per Apply.
	if z := cachedZone(t, h.store, "work.example"); z.Serial != liveSerial+1 {
		t.Fatalf("cached serial = %d, want %d", z.Serial, liveSerial+1)
	}
}

func TestAnApplyWhoseReCheckFailsIsNotSent(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.buildChangeset()

	f.Fail(fake.OpRecords, errors.New("connection reset by peer"))
	h.press("p", "enter")

	if calls := applyCalls(f); len(calls) != 0 {
		t.Fatalf("a Changeset that could not be re-checked was sent: %+v", calls)
	}

	h.assertShows("Changes to work.example", "Could not apply the changes to work.example: re-check the Records: connection reset by peer")
}
