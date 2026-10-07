package tui_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
)

// mailRecords are work.example's Records with mail already set up: an
// MX, an SPF and a DMARC Record, next to Records no Template owns.
var mailRecords = []dns.Record{
	{ProviderID: "rm1", Type: "A", Name: "work.example", Value: "203.0.113.20", TTL: 300},
	{ProviderID: "rm2", Type: "MX", Name: "work.example", Value: "10 mail.work.example", TTL: 3600},
	{ProviderID: "rm3", Type: "TXT", Name: "work.example", Value: "v=spf1 mx -all", TTL: 3600},
	{ProviderID: "rm4", Type: "TXT", Name: "_dmarc.work.example", Value: "v=DMARC1; p=reject", TTL: 3600},
	{ProviderID: "rm5", Type: "TXT", Name: "work.example", Value: "google-site-verification=xyz", TTL: 3600},
}

// mailZones scripts the fake Provider with work.example holding mailRecords.
func mailZones() *fake.Provider {
	return fake.New().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial}, mailRecords...).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2"})
}

// Templates in the order the Template dialog offers them.
const (
	googleMail = iota
	protonMail
	parkedDomain
)

// Fields of each Template's dialog, the Template choice included.
const (
	parkedFields = 1
	googleFields = 5
	protonFields = 7
)

// openTemplate opens the Template dialog and picks the nth Template.
func (h *harness) openTemplate(n int) {
	h.t.Helper()

	h.press("t")
	h.assertShows("Apply a Template")

	for range n {
		h.press("down")
	}
}

// enter presses Enter n times: in the Template dialog, it moves to the
// next field, and on the last field it submits.
func (h *harness) enter(n int) {
	h.t.Helper()

	for range n {
		h.press("enter")
	}
}

func modifyTo(before dns.Record, value string, ttl uint32) dns.Change {
	after := before
	after.Value, after.TTL = value, ttl

	return dns.Change{Kind: dns.ChangeModify, Before: before, After: after}
}

func addOf(typ, name, value string, ttl uint32) dns.Change {
	return dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: typ, Name: name, Value: value, TTL: ttl}}
}

func TestParkedDomainTemplateBuildsAChangesetThatIsPreviewedAndApplied(t *testing.T) {
	f := refreshZones()
	h := openValidated(t, f)

	h.openTemplate(parkedDomain)
	h.assertShows("‹ Parked domain ›", "null MX")
	h.enter(parkedFields)

	h.assertHides("Apply a Template")
	h.assertShows("4 pending changes", "v=spf1 -all")

	if calls := applyCalls(f); len(calls) != 0 {
		t.Fatalf("a Template applied changes without a preview: %+v", calls)
	}

	h.press("p")
	h.assertShows("Changes to work.example",
		"~ @ MX 10 mail.work.example TTL 3600 → @ MX 0 . TTL 3600",
		"+ @ TXT v=spf1 -all TTL 3600",
		"+ _dmarc TXT v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s TTL 3600",
		"+ *._domainkey TXT v=DKIM1; p= TTL 3600")
	h.press("enter")

	calls := applyCalls(f)
	if len(calls) != 1 {
		t.Fatalf("Apply calls = %+v", calls)
	}

	// The fake Provider has no automatic TTL, so the Records get an hour.
	want := []dns.Change{
		modifyTo(liveRecords[1], "0 .", 3600),
		addOf("TXT", "work.example", "v=spf1 -all", 3600),
		addOf("TXT", "_dmarc.work.example", "v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s", 3600),
		addOf("TXT", "*._domainkey.work.example", "v=DKIM1; p=", 3600),
	}
	if !reflect.DeepEqual(calls[0].Changes, want) {
		t.Fatalf("applied changes =\n%+v\nwant\n%+v", calls[0].Changes, want)
	}

	h.assertShows("Applied 4 changes to work.example.")
}

func TestGoogleMailTemplateReplacesSPFAndDMARCInsteadOfAddingThem(t *testing.T) {
	f := mailZones()
	f.SetAutoTTL(true)
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	h.openTemplate(googleMail)
	h.assertShows("‹ Google mail ›", "DMARC policy", "DKIM selector", "google", "DKIM key")
	h.enter(googleFields)

	h.assertShows("3 pending changes")
	h.press("p", "enter")

	// The Provider has an automatic TTL, so the Template uses it.
	want := []dns.Change{
		modifyTo(mailRecords[1], "1 smtp.google.com", 1),
		modifyTo(mailRecords[2], "v=spf1 include:_spf.google.com ~all", 1),
		modifyTo(mailRecords[3], "v=DMARC1; p=none", 1),
	}

	calls := applyCalls(f)
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Changes, want) {
		t.Fatalf("Apply calls = %+v\nwant changes %+v", calls, want)
	}

	cached := cachedRecordsOf(t, h, "work.example")

	var spf, dmarc int

	for _, r := range cached {
		switch {
		case r.Type == "TXT" && r.Name == "work.example" && strings.HasPrefix(r.Value, "v=spf1"):
			spf++
		case r.Name == "_dmarc.work.example":
			dmarc++
		}
	}

	if spf != 1 || dmarc != 1 || len(cached) != len(mailRecords) {
		t.Fatalf("cached Records = %+v: want one SPF, one DMARC and nothing else added", cached)
	}

	// Applying the Template again finds nothing to change.
	h.openTemplate(googleMail)
	h.enter(googleFields)
	h.assertShows("work.example already has the Records of Google mail")
	h.assertHides("Apply a Template", "pending change")
}

func TestGoogleMailTemplateTakesADMARCPolicyAndADKIMKey(t *testing.T) {
	f := mailZones()
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	h.openTemplate(googleMail)
	h.press("tab", "down", "down") // DMARC policy: none → quarantine → reject
	h.assertShows("‹ reject ›")
	h.press("tab")
	h.typeText("dmarc@work.example")
	h.press("tab", "tab")
	h.typeText("MIIBIjANBgkq")
	h.press("enter")

	h.assertShows("4 pending changes")
	h.press("p", "enter")

	want := []dns.Change{
		modifyTo(mailRecords[1], "1 smtp.google.com", 3600),
		modifyTo(mailRecords[2], "v=spf1 include:_spf.google.com ~all", 3600),
		modifyTo(mailRecords[3], "v=DMARC1; p=reject; rua=mailto:dmarc@work.example", 3600),
		addOf("TXT", "google._domainkey.work.example", "v=DKIM1; k=rsa; p=MIIBIjANBgkq", 3600),
	}

	calls := applyCalls(f)
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Changes, want) {
		t.Fatalf("Apply calls = %+v\nwant changes %+v", calls, want)
	}
}

func TestATemplateParameterErrorKeepsTheDialogOpen(t *testing.T) {
	h := openValidatedShowing(t, mailZones(), "v=spf1 mx -all")

	h.openTemplate(googleMail)
	h.press("tab", "tab")
	h.typeText("not an address")
	h.enter(googleFields - 2)

	h.assertShows("Apply a Template", "is not an email address")
	h.assertHides("pending change")
}

func TestTheTemplateDialogFitsASmallTerminal(t *testing.T) {
	h := openValidatedShowing(t, mailZones(), "v=spf1 mx -all")
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})

	h.openTemplate(protonMail)
	h.enter(protonFields)

	h.assertShows("Enter the Verification token", "Esc cancel")
	h.assertDialogFits("Apply a Template to work.example")
}

func TestProtonMailTemplateAsksForItsTokenAndDKIMTargets(t *testing.T) {
	f := mailZones()
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	h.openTemplate(protonMail)
	h.assertShows("‹ Proton mail ›", "Verification token", "DKIM target 1")

	// Every parameter but the DMARC ones is required.
	h.enter(protonFields)
	h.assertShows("Apply a Template", "Enter the Verification token")
	h.assertHides("pending change")

	h.typeText("protonmail-verification=abc123")
	h.press("tab")
	h.typeText("protonmail.domainkey.dx.domains.proton.ch")
	h.press("tab")
	h.typeText("protonmail2.domainkey.dx.domains.proton.ch")
	h.press("tab")
	h.typeText("protonmail3.domainkey.dx.domains.proton.ch")
	h.enter(protonFields - 4) // from DKIM target 3, past the DMARC fields

	h.assertHides("Apply a Template")
	h.press("p", "enter")

	want := []dns.Change{
		addOf("TXT", "work.example", "protonmail-verification=abc123", 3600),
		{Kind: dns.ChangeDelete, Before: mailRecords[1]},
		addOf("MX", "work.example", "10 mail.protonmail.ch", 3600),
		addOf("MX", "work.example", "20 mailsec.protonmail.ch", 3600),
		modifyTo(mailRecords[2], "v=spf1 include:_spf.protonmail.ch ~all", 3600),
		addOf("CNAME", "protonmail._domainkey.work.example", "protonmail.domainkey.dx.domains.proton.ch", 3600),
		addOf("CNAME", "protonmail2._domainkey.work.example", "protonmail2.domainkey.dx.domains.proton.ch", 3600),
		addOf("CNAME", "protonmail3._domainkey.work.example", "protonmail3.domainkey.dx.domains.proton.ch", 3600),
		modifyTo(mailRecords[3], "v=DMARC1; p=none", 3600),
	}

	calls := applyCalls(f)
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Changes, want) {
		t.Fatalf("Apply calls = %+v\nwant changes %+v", calls, want)
	}
}

func TestATemplateDoesNotOverwriteAChangeTheUserStaged(t *testing.T) {
	f := mailZones()
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	// The user edits the SPF Record, the last row, by hand.
	h.press("down", "down", "down", "down", "e", "tab", "tab")
	h.clearText(len("-all"))
	h.typeText("~all")
	h.press("enter", "enter")
	h.assertShows("1 pending change", "v=spf1 mx ~all")

	h.openTemplate(parkedDomain)
	h.enter(parkedFields)

	h.assertShows("Apply a Template", "Nothing was staged", "Parked domain would change", "@ TXT v=spf1 mx ~all TTL 3600")
	h.press("esc")
	h.assertShows("1 pending change", "v=spf1 mx ~all")

	// Discarding the edit lets the Template replace the Record.
	h.press("p", "D")
	h.assertShows("Discarded 1 pending change", "v=spf1 mx -all")
	h.openTemplate(parkedDomain)
	h.enter(parkedFields)
	h.assertHides("Apply a Template")
	h.assertShows("4 pending changes", "v=spf1 -all")
}

func TestATemplateKeepsAChangeTheUserStagedThatItAgreesWith(t *testing.T) {
	f := mailZones()
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	// The user deletes the SPF Record by hand: the Template adds its own.
	h.press("down", "down", "down", "down", "d")
	h.assertShows("1 pending change")

	h.openTemplate(parkedDomain)
	h.enter(parkedFields)
	h.assertHides("Apply a Template")
	h.press("p", "enter")

	want := []dns.Change{
		{Kind: dns.ChangeDelete, Before: mailRecords[2]},
		modifyTo(mailRecords[1], "0 .", 3600),
		addOf("TXT", "work.example", "v=spf1 -all", 3600),
		modifyTo(mailRecords[3], "v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s", 3600),
		addOf("TXT", "*._domainkey.work.example", "v=DKIM1; p=", 3600),
	}

	calls := applyCalls(f)
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Changes, want) {
		t.Fatalf("Apply calls = %+v\nwant changes %+v", calls, want)
	}
}

func TestTheWriteGateRefusesTemplatesOnAStaleZone(t *testing.T) {
	f := refreshZones()
	h, _, _ := newRefreshHarness(t, f)

	f.Fail(fake.OpRecords, errors.New("connection reset by peer"))
	h.press("enter", "shift+tab")
	h.assertShows("Refresh failed")

	h.press("t")

	h.assertShows("work.example is a Stale Zone")
	h.assertHides("Apply a Template")
}

func TestTheWriteGateRefusesTemplatesInASealedSession(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := refreshZones()
	h := newAccountHarness(t, s, f)

	h.press("enter")
	h.press("esc", "shift+tab")

	h.press("t")

	h.assertShows("The session is sealed and read-only")
	h.assertHides("Apply a Template")
}
