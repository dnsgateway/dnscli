package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// recordsCell returns the Records pane's cell on the line that mentions
// text.
func (h *harness) recordsCell(text string) string {
	h.t.Helper()

	for _, line := range strings.Split(ansi.Strip(h.rawView()), "\n") {
		cells := strings.FieldsFunc(line, func(r rune) bool { return r == '│' || r == '┃' })
		if len(cells) > 1 && strings.Contains(cells[1], text) {
			return cells[1]
		}
	}

	h.t.Fatalf("no Records row for %q:\n%s", text, h.view())

	return ""
}

// openProxied opens work.example, validated, with its A and MX Records
// and a proxied CNAME carrying a free-text comment too.
func openProxied(t *testing.T) *harness {
	t.Helper()

	proxied := dns.Record{ProviderID: "rw3", Type: "CNAME", Name: "www.work.example", Value: "work.example", TTL: 300,
		Extras: map[string]string{"proxied": "true", "comment": "kept"}}
	unproxied := dns.Record{ProviderID: "rw4", Type: "A", Name: "api.work.example", Value: "192.0.2.7", TTL: 300,
		Extras: map[string]string{"proxied": "false"}}

	f := newFake().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial},
		liveRecords[0], liveRecords[1], proxied, unproxied)
	f.SetProxiableTypes("A", "AAAA", "CNAME")

	return openValidated(t, f)
}

func TestAFlagExtraOfTheOpenZonesRecordsGetsAColumnSayingYesOrNo(t *testing.T) {
	h := openProxied(t)

	if row := strings.TrimRight(h.recordsCell("Content"), " "); !strings.HasSuffix(row, "TTL    Proxied") {
		t.Fatalf("header = %q, want a Proxied column after TTL", row)
	}

	for text, want := range map[string]string{"www.work.example": "yes", "api.work.example": "no"} {
		if row := strings.TrimRight(h.recordsCell(text), " "); !strings.HasSuffix(row, " "+want) {
			t.Errorf("%s row = %q, want Proxied %q", text, row, want)
		}
	}

	// A Record without the extra leaves its cell blank.
	if row := strings.TrimRight(h.recordsCell("mail.work.example"), " "); !strings.HasSuffix(row, "3600") {
		t.Errorf("MX row = %q, want nothing after its TTL", row)
	}

	// Free text, such as a comment, gets no column.
	h.assertHides("kept", "comment")
}

func TestAZoneWhoseRecordsHaveNoListedExtraHasNoExtraColumn(t *testing.T) {
	h := openValidated(t, refreshZones())

	if row := strings.TrimRight(h.recordsCell("Content"), " "); !strings.HasSuffix(row, "TTL") {
		t.Fatalf("header = %q, want TTL last", row)
	}

	h.assertHides("Proxied")
}

func TestAnExtraColumnGivesWayToNameAndContentInANarrowPane(t *testing.T) {
	h := openProxied(t)

	h.send(tea.WindowSizeMsg{Width: 60, Height: 30})

	h.assertHides("Proxied")

	if row := h.recordsCell("CNAME"); !strings.Contains(row, "www") || strings.Contains(row, "yes") {
		t.Fatalf("narrow row = %q, want the CNAME without its Proxied cell", row)
	}
}

// openCommented opens work.example, validated, with its A and MX Records
// and a CNAME carrying a Cloudflare comment and tags.
func openCommented(t *testing.T) *harness {
	t.Helper()

	commented := dns.Record{ProviderID: "rw3", Type: "CNAME", Name: "www.work.example", Value: "work.example", TTL: 300,
		Extras: map[string]string{"proxied": "true", "comment": "fronted by the CDN since the 2025 migration", "tags": `["env:prod","team:web"]`}}

	f := newFake().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial},
		liveRecords[0], liveRecords[1], commented)
	f.SetProxiableTypes("A", "AAAA", "CNAME")

	return openValidated(t, f)
}

func TestTheRecordFormShowsTheCommentAndTagsOfTheRecordItEdits(t *testing.T) {
	h := openCommented(t)

	h.press("j", "j", "e")
	h.assertShows("Edit Record", "‹ CNAME ›", "Comment", "fronted by the CDN", "migration", "Tags", "env:prod", "team:web")

	// They are shown, not edited: Tab steps from Proxied to Save.
	h.press("tab", "tab", "tab", "tab")
	h.assertShows("› Proxied")
	h.press("tab")
	h.assertShows("› [ Save ]")
	h.assertHides("› Comment")
	h.press("esc")

	// A Record without them shows neither, nor does a new one.
	h.press("k", "e")
	h.assertShows("Edit Record", "‹ MX ›")
	h.assertHides("Comment", "Tags")
	h.press("esc", "i")
	h.assertShows("Add Record")
	h.assertHides("Comment", "Tags")
}

func TestEditingARecordKeepsItsCommentAndTags(t *testing.T) {
	h := openCommented(t)

	h.press("j", "j", "e", "tab", "tab")
	h.clearText(len("work.example"))
	h.typeText("work.test.")
	h.saveForm()

	h.press("e")
	h.assertShows("Edit Record", "work.test.", "fronted by the CDN", "env:prod")
}

// A comment of blanks only is not shown, and opening its Record does not fail.
func TestABlankCommentIsNotShown(t *testing.T) {
	blank := dns.Record{ProviderID: "rw3", Type: "CNAME", Name: "www.work.example", Value: "work.example", TTL: 300,
		Extras: map[string]string{"comment": " \n "}}

	f := newFake().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial},
		liveRecords[0], liveRecords[1], blank)

	h := openValidated(t, f)

	h.press("j", "j", "e")
	h.assertShows("Edit Record", "‹ CNAME ›")
	h.assertHides("Comment")
}
