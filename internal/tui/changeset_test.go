package tui_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
)

// openValidated opens work.example in an unsealed session, lets its
// Refresh validate it, and focuses the Records pane. The Records pane then
// lists liveRecords: the A Record first, then the MX Record.
func openValidated(t *testing.T, f *fake.Provider) *harness {
	t.Helper()

	return openValidatedShowing(t, f, "203.0.113.20")
}

// openValidatedShowing is openValidated for a Zone scripted in f with
// other Records, one of which shows text.
func openValidatedShowing(t *testing.T, f *fake.Provider, text string) *harness {
	t.Helper()

	h, _, _ := newRefreshHarness(t, f)
	h.press("enter", "shift+tab")
	h.assertShows("validated this session", text)

	return h
}

func TestTheWriteGateRefusesEditsOnAStaleZone(t *testing.T) {
	f := refreshZones()
	h, _, _ := newRefreshHarness(t, f)

	f.Fail(fake.OpRecords, errors.New("connection reset by peer"))
	h.press("enter", "shift+tab")
	h.assertShows("Refresh failed")

	for _, key := range []string{"i", "e", "d"} {
		h.press(key)

		h.assertShows("work.example is a Stale Zone", "a Refresh validates it")
		h.assertHides("Add Record", "Edit Record")
	}

	h.press("p")
	h.assertShows("No pending changes for work.example.")
}

func TestTheWriteGateRefusesEditsInASealedSessionAndOffersUnseal(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := refreshZones()
	h := newAccountHarness(t, s, f)

	// Opening the Zone asks to Unseal for its Refresh; the user declines.
	h.press("enter")
	h.assertShows("Unseal", "Passphrase")
	h.press("esc", "shift+tab")

	h.press("i")

	h.assertShows("The session is sealed and read-only: Unseal to edit work.example.", "Unseal", "Passphrase")
	h.assertHides("Add Record")

	// Unsealing Refreshes the Zone, which validates it for editing.
	h.typeText(passphrase)
	h.press("enter")

	h.assertShows("validated this session", "203.0.113.20")

	h.press("i")
	h.assertShows("Add Record")

	if calls := applyCalls(f); len(calls) != 0 {
		t.Fatalf("Apply calls = %+v", calls)
	}
}

// clearText presses Backspace n times.
func (h *harness) clearText(n int) {
	h.t.Helper()

	for range n {
		h.press("backspace")
	}
}

// addRecord fills the Record form for a new Record: it steps the type
// choice down to typ, then sets the name, value and TTL.
func (h *harness) addRecord(typ, name, value, ttl string) {
	h.t.Helper()

	h.press("i")
	h.assertShows("Add Record")

	for range slices.Index(dns.EditableTypes, typ) {
		h.press("down")
	}

	h.press("tab")
	h.typeText(name)
	h.press("tab")
	h.typeText(value)
	h.press("tab")
	h.clearText(len("300"))
	h.typeText(ttl)
	h.press("enter")
}

func TestAddingARecordBuildsAPendingAdditionMarkedInTheRecordsPane(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)

	h.press("i")
	h.assertShows("Add Record", "Type", "‹ A ›", "Name", "Value", "TTL", "300")
	h.press("esc")

	h.addRecord("TXT", "_verify", "token-123", "3600")

	h.assertHides("Add Record")
	h.assertShows("+ _verify.work.example", "token-123", "1 pending change")

	if calls := applyCalls(f); len(calls) != 0 {
		t.Fatalf("building a Changeset applied it: %+v", calls)
	}
}

func TestTheRecordFormRejectsBadInput(t *testing.T) {
	cases := []struct {
		typ, name, value, ttl string
		want                  string
	}{
		{"A", "www", "not-an-ip", "300", "An A value is an IPv4 address"},
		{"A", "www", "2001:db8::1", "300", "An A value is an IPv4 address"},
		{"AAAA", "www", "192.0.2.1", "300", "An AAAA value is an IPv6"},
		{"CNAME", "www", "has space.example", "300", "A CNAME value is a host name"},
		{"MX", "@", "mail.work.example", "300", "An MX value is a preference"},
		{"NS", "sub", "ns1..example", "300", "An NS value is a host name"},
		{"SRV", "_sip._tcp", "10 5 host.example", "300", "An SRV value is a priority"},
		{"CAA", "@", "issue letsencrypt.org", "300", "A CAA value is flags"},
		{"URI", "_ftp._tcp", "10 1 ftp://example.com/", "300", "A URI value is a priority"},
		{"TXT", "@", "", "300", "Enter a value"},
		{"A", "www", "192.0.2.1", "0", "The TTL must be 1 (automatic)"},
		{"A", "www", "192.0.2.1", "86401", "The TTL must be 1"},
		{"A", "www", "192.0.2.1", "ten", "The TTL must be 1"},
		{"A", "www.other.example.", "192.0.2.1", "300", "not inside the Zone"},
		{"A", "bad name", "192.0.2.1", "300", "The name bad name"},
	}

	for _, c := range cases {
		t.Run(c.typ+" "+c.value+" "+c.ttl, func(t *testing.T) {
			h := openValidated(t, refreshZones())

			h.addRecord(c.typ, c.name, c.value, c.ttl)

			h.assertShows("Add Record", c.want)
			h.assertHides("1 pending change")
		})
	}
}

func TestTheRecordFormTakesEveryEditableTypeWithRelativeOrAbsoluteNames(t *testing.T) {
	h := openValidated(t, refreshZones())

	h.addRecord("A", "api.work.example.", "192.0.2.1", "1")
	h.addRecord("AAAA", "v6", "2001:db8::1", "300")
	h.addRecord("CNAME", "www.work.example", "work.example", "300")
	h.addRecord("MX", "@", "0 .", "300")
	h.addRecord("TXT", "@", "v=spf1 -all", "300")
	h.addRecord("NS", "sub", "ns1.example.net", "86400")
	h.addRecord("SRV", "_sip._tcp", "10 5 5060 sip.work.example", "30")
	h.addRecord("CAA", "@", `0 issue "letsencrypt.org"`, "300")
	h.addRecord("URI", "_ftp._tcp", `10 1 "ftp://ftp.work.example/"`, "300")

	h.assertHides("Add Record")
	h.assertShows("9 pending changes",
		"+ api.work.example", "+ v6.work.example", "+ www.work.example", "+ sub.work.example",
		"+ _sip._tcp.work.example", "+ _ftp._tcp.work.example", "0 .", `0 issue "letsencrypt.org"`)
}

func TestEditingARecordPrefillsTheFormAndMarksItModified(t *testing.T) {
	h := openValidated(t, refreshZones())

	h.press("e")
	h.assertShows("Edit Record", "‹ A ›", "@", "203.0.113.20", "300")

	// Tab to the value and change it.
	h.press("tab", "tab")
	h.clearText(len("20"))
	h.typeText("99")
	h.press("enter", "enter")

	h.assertHides("Edit Record", "203.0.113.20")
	h.assertShows("~ work.example", "203.0.113.99", "1 pending change")

	// Editing it back to the cached Record drops the change.
	h.press("e", "tab", "tab")
	h.clearText(len("99"))
	h.typeText("20")
	h.press("enter", "enter")

	h.assertShows("203.0.113.20")
	h.assertHides("~ work.example", "pending change")
}

func TestDeletingMarksARecordAndDeletingAgainTakesTheChangeBack(t *testing.T) {
	h := openValidated(t, refreshZones())

	h.press("j", "d")
	h.assertShows("- work.example", "10 mail.work.example", "1 pending change")

	h.press("d")
	h.assertHides("- work.example", "pending change")

	// Deleting a pending addition drops it.
	h.addRecord("TXT", "@", "hello", "300")
	h.assertShows("+ work.example")

	h.press("j", "j", "d")
	h.assertHides("+ work.example", "hello", "pending change")

	// The cursor moves up to the row that is now last.
	h.press("e")
	h.assertShows("Edit Record", "‹ MX ›", "10 mail.work.example")
}

// buildChangeset stages, on work.example, an addition, a modification of
// its A Record and a deletion of its MX Record.
func (h *harness) buildChangeset() {
	h.t.Helper()

	h.addRecord("TXT", "_verify", "token-123", "3600")
	h.press("e", "tab", "tab")
	h.clearText(len("20"))
	h.typeText("99")
	h.press("enter", "enter")
	h.press("j", "d")
	h.assertShows("3 pending changes")
}

func TestThePreviewShowsEveryChangeAndDiscardingDropsThem(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.buildChangeset()

	h.press("p")

	h.assertShows("Changes to work.example", "Applying sends 3 pending changes",
		"+ _verify TXT token-123 TTL 3600",
		"~ @ A 203.0.113.20 TTL 300 → @ A 203.0.113.99 TTL 300",
		"- @ MX 10 mail.work.example TTL 3600")

	// Closing the preview keeps the changes.
	h.press("esc")
	h.assertHides("Changes to work.example")
	h.assertShows("3 pending changes")

	h.press("p", "D")

	h.assertHides("Changes to work.example", "token-123", "203.0.113.99", "(p preview)", "+ ", "~ ")
	h.assertShows("Discarded 3 pending changes for work.example.", "203.0.113.20", "10 mail.work.example")

	if calls := applyCalls(f); len(calls) != 0 {
		t.Fatalf("Apply calls = %+v", calls)
	}
}

func TestApplyingSendsTheChangesetAndCachesTheProvidersRecords(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.buildChangeset()

	h.press("p", "enter")

	calls := applyCalls(f)
	if len(calls) != 1 {
		t.Fatalf("Apply calls = %+v", calls)
	}

	cs := calls[0]
	want := []dns.Change{
		{Kind: dns.ChangeAdd, After: dns.Record{Type: "TXT", Name: "_verify.work.example", Value: "token-123", TTL: 3600}},
		{Kind: dns.ChangeModify, Before: liveRecords[0], After: dns.Record{ProviderID: "rw1", Type: "A", Name: "work.example", Value: "203.0.113.99", TTL: 300}},
		{Kind: dns.ChangeDelete, Before: liveRecords[1]},
	}

	if cs.Zone.Name != "work.example" || cs.Zone.ProviderID != "zw1" || !reflect.DeepEqual(cs.Changes, want) {
		t.Fatalf("applied Changeset = %+v, want changes %+v", cs, want)
	}

	h.assertHides("Changes to work.example", "pending change", "203.0.113.20", "10 mail.work.example")
	h.assertShows("Applied 3 changes to work.example.", "_verify.work.example", "token-123", "203.0.113.99")

	// The cache holds the Records as the Provider stored them, with the ID
	// it gave the new one, without another Refresh.
	records, err := h.store.Records(context.Background(), zoneID(t, h.store, "work.example"))
	if err != nil {
		t.Fatal(err)
	}

	wantCached := []dns.Record{
		{ProviderID: "fake-1", Type: "TXT", Name: "_verify.work.example", Value: "token-123", TTL: 3600},
		{ProviderID: "rw1", Type: "A", Name: "work.example", Value: "203.0.113.99", TTL: 300},
	}
	if !reflect.DeepEqual(records, wantCached) {
		t.Fatalf("cached Records = %+v, want %+v", records, wantCached)
	}

	if n := recordsCalls(f, "zw1"); n != 1 {
		t.Fatalf("Records calls = %d, want 1", n)
	}

	// The Zone stays validated, so editing goes on.
	h.assertMarker("work.example", "✓")
	h.press("i")
	h.assertShows("Add Record")
}

func TestAFailedApplyKeepsTheChangesPendingAndTheCacheAsItWas(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)
	h.buildChangeset()

	f.Fail(fake.OpApply, errors.New("not implemented"))
	h.press("p", "enter")

	h.assertShows("Changes to work.example", "Could not apply the changes to work.example: not implemented")

	h.press("esc")
	h.assertShows("+ _verify.work.example", "~ work.example", "- work.example")

	if got := cachedRecordValues(t, h.store, "work.example"); !slices.Equal(got, []string{"203.0.113.20", "10 mail.work.example"}) {
		t.Fatalf("cached records = %q", got)
	}
}

func TestTheHelpOverlayListsTheRecordKeys(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarness(t, s)
	h.press("?")

	h.assertShows("insert a new Record", "edit the Record", "delete the Record",
		"preview the pending changes", "discard the pending changes")
}

// applyCalls returns the Changesets the fake Provider was asked to apply.
func applyCalls(f *fake.Provider) []dns.Changeset {
	var out []dns.Changeset

	for _, c := range f.Calls() {
		if c.Op == fake.OpApply {
			out = append(out, c.Changeset)
		}
	}

	return out
}
