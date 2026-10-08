package tui_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
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
func mailZones() *fakeProvider {
	return newFake().
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
		h.press("right")
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
	h.saveForm()

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
	h.press("tab", "tab")
	h.typeText("dmarc@work.example")
	h.saveForm()

	h.assertShows("3 pending changes")
	h.press("p", "enter")

	// The Provider has an automatic TTL, so the Template uses it.
	want := []dns.Change{
		modifyTo(mailRecords[1], "1 smtp.google.com", 1),
		modifyTo(mailRecords[2], "v=spf1 include:_spf.google.com ~all", 1),
		modifyTo(mailRecords[3], "v=DMARC1; p=reject; rua=mailto:dmarc@work.example", 1),
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
	h.saveForm()
	h.assertShows("work.example already has the Records of Google mail")
	h.assertHides("Apply a Template", "pending change")
}

func TestGoogleMailTemplateTakesADMARCPolicyAndADKIMKey(t *testing.T) {
	f := mailZones()
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	h.openTemplate(googleMail)
	h.press("tab") // DMARC policy: prefilled from the Zone's DMARC
	h.assertShows("‹ reject ›")
	h.press("tab")
	h.typeText("dmarc@work.example")
	h.press("tab", "tab")
	h.typeText("MIIBIjANBgkq")
	h.saveForm()

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
	h.saveForm()

	h.assertShows("Apply a Template", "is not an email address")
	h.assertHides("pending change")
}

func TestTheTemplateDialogFitsASmallTerminal(t *testing.T) {
	h := openValidatedShowing(t, mailZones(), "v=spf1 mx -all")
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})

	h.openTemplate(protonMail)
	h.saveForm()

	h.assertShows("Enter the Verification token", "[ Save ]", "Esc cancel")
	h.assertDialogFits("Apply a Template to work.example")
}

func TestProtonMailTemplateAsksForItsTokenAndDKIMTargets(t *testing.T) {
	f := mailZones()
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	h.openTemplate(protonMail)
	h.assertShows("‹ Proton mail ›", "Verification token", "DKIM target 1")

	// Every parameter but the DMARC ones is required.
	h.saveForm()
	h.assertShows("Apply a Template", "Enter the Verification token")
	h.assertHides("pending change")

	h.typeText("protonmail-verification=abc123")
	h.press("tab")
	h.typeText("protonmail.domainkey.dx.domains.proton.ch")
	h.press("tab")
	h.typeText("protonmail2.domainkey.dx.domains.proton.ch")
	h.press("tab")
	h.typeText("protonmail3.domainkey.dx.domains.proton.ch")
	h.saveForm()

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
	h.saveForm()
	h.assertShows("1 pending change", "v=spf1 mx ~all")

	h.openTemplate(parkedDomain)
	h.saveForm()

	h.assertShows("Apply a Template", "Nothing was staged", "Parked domain would change", "@ TXT v=spf1 mx ~all TTL 3600")
	h.press("esc")
	h.assertShows("1 pending change", "v=spf1 mx ~all")

	// Discarding the edit lets the Template replace the Record.
	h.press("p", "D")
	h.assertShows("Discarded 1 pending change", "v=spf1 mx -all")
	h.openTemplate(parkedDomain)
	h.saveForm()
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
	h.saveForm()
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

func TestATemplateDoesNotModifyARecordIntoOneStagedForDeletion(t *testing.T) {
	strict := dns.Record{ProviderID: "rm6", Type: "TXT", Name: "work.example", Value: "v=spf1 -all", TTL: 3600}
	f := newFake().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial},
		mailRecords[0], mailRecords[2], strict)
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	// The user deletes the strict SPF; the Template would turn the other
	// SPF into it.
	h.press("down", "d")
	h.press("p")
	h.assertShows("- @ TXT v=spf1 -all")
	h.press("esc")

	h.openTemplate(parkedDomain)
	h.saveForm()

	h.assertShows("Apply a Template", "Nothing was staged", "@ TXT v=spf1 -all TTL 3600 (pending deletion)")
	h.press("esc")
	h.assertShows("1 pending change")
}

func TestATemplateDoesNotChangeARecordWithoutAProviderID(t *testing.T) {
	noID := dns.Record{Type: "TXT", Name: "work.example", Value: "v=spf1 mx -all", TTL: 3600}
	f := newFake().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial}, mailRecords[0], noID)
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	h.openTemplate(parkedDomain)
	h.saveForm()

	h.assertShows("Apply a Template", "Nothing was staged", "no Provider ID")
	h.assertHides("pending change")
}

func TestTheNoProviderIDErrorListsNoConflicts(t *testing.T) {
	strict := dns.Record{ProviderID: "rm6", Type: "TXT", Name: "work.example", Value: "v=spf1 -all", TTL: 3600}
	noID := dns.Record{Type: "TXT", Name: "_dmarc.work.example", Value: "v=DMARC1; p=none", TTL: 3600}
	f := newFake().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial},
		mailRecords[0], mailRecords[2], strict, noID)
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	// The SPF the Template would bring back is staged for deletion, and
	// the DMARC it would change has no Provider ID.
	h.press("down", "down", "d")
	h.press("p")
	h.assertShows("- @ TXT v=spf1 -all")
	h.press("esc")

	h.openTemplate(parkedDomain)
	h.saveForm()

	h.assertShows("Apply a Template", "Nothing was staged", "no Provider ID")
	h.assertHides("(pending deletion)")
}

func TestTheWriteGateRefusesTemplatesOnAStaleZone(t *testing.T) {
	f := refreshZones()
	h, _, _ := newRefreshHarness(t, f)

	f.Fail(providertest.OpRecords, errors.New("connection reset by peer"))
	h.press("enter", "tab")
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
	h.press("esc", "tab")

	h.press("t")

	h.assertShows("The session is sealed and read-only")
	h.assertHides("Apply a Template")
}

func TestAMailTemplateKeepsTheZonesDMARCUnlessItIsEdited(t *testing.T) {
	hardened := dns.Record{ProviderID: "rm4", Type: "TXT", Name: "_dmarc.work.example",
		Value: "v=DMARC1; p=quarantine; sp=reject; rua=mailto:a@work.example; adkim=s", TTL: 3600}
	f := newFake().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial},
		mailRecords[0], mailRecords[1], mailRecords[2], hardened)
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	h.openTemplate(googleMail)
	h.assertShows("DMARC policy      quarantine", "mailto:a@work.example")
	h.saveForm()

	h.assertShows("2 pending changes")
	h.press("p", "enter")

	want := []dns.Change{
		modifyTo(mailRecords[1], "1 smtp.google.com", 3600),
		modifyTo(mailRecords[2], "v=spf1 include:_spf.google.com ~all", 3600),
	}

	calls := applyCalls(f)
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Changes, want) {
		t.Fatalf("Apply calls = %+v\nwant changes %+v", calls, want)
	}
}

func TestAWeakerDMARCPolicyNeedsASecondSave(t *testing.T) {
	f := mailZones()
	h := openValidatedShowing(t, f, "v=spf1 mx -all")

	h.openTemplate(googleMail)
	h.press("tab", "right") // DMARC policy: reject → none
	h.assertShows("‹ none ›")
	h.saveForm()

	h.assertShows("Apply a Template", "The DMARC policy of work.example is reject: none is", "Save again")
	h.assertHides("pending change")

	h.press("enter")
	h.assertHides("Apply a Template")
	h.assertShows("3 pending changes")
	h.press("p", "enter")

	want := []dns.Change{
		modifyTo(mailRecords[1], "1 smtp.google.com", 3600),
		modifyTo(mailRecords[2], "v=spf1 include:_spf.google.com ~all", 3600),
		modifyTo(mailRecords[3], "v=DMARC1; p=none", 3600),
	}

	calls := applyCalls(f)
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Changes, want) {
		t.Fatalf("Apply calls = %+v\nwant changes %+v", calls, want)
	}
}

func TestEditingAfterTheWeakerPolicyWarningAsksAgain(t *testing.T) {
	h := openValidatedShowing(t, mailZones(), "v=spf1 mx -all")

	h.openTemplate(googleMail)
	h.press("tab", "right", "right") // DMARC policy: reject → none → quarantine
	h.saveForm()
	h.assertShows("is reject: quarantine")

	// Changing the policy, even back to the one warned of, warns again
	// before anything is staged.
	h.press("up", "up", "up", "up", "left", "right") // from Save to the DMARC policy
	h.assertShows("‹ quarantine ›")
	h.saveForm()
	h.assertShows("Apply a Template", "is reject: quarantine")
	h.assertHides("pending change")
}

func TestTheTemplateDialogTakesTheSameKeysAsTheRecordForm(t *testing.T) {
	h := openValidatedShowing(t, mailZones(), "v=spf1 mx -all")

	h.press("t")
	h.assertShows("› Template", "‹ Google mail ›")

	// Left and Right change the Template, round from the first to the last.
	h.press("left")
	h.assertShows("‹ Parked domain ›")
	h.press("right")
	h.assertShows("‹ Google mail ›")

	// Enter lists every Template; Up and Down pick one.
	h.press("enter")
	h.assertPicked("Google mail")
	h.press("down")
	h.assertPicked("Proton mail")
	h.press("enter")
	h.assertShows("› Template", "‹ Proton mail ›")

	// Up and Down move between the fields, round from the first to Save.
	h.press("up")
	h.assertShows("› [ Save ]")
	h.press("up", "up") // the DMARC policy, prefilled from the Zone's DMARC
	h.assertShows("‹ reject ›")

	// A choice parameter has a picker too.
	h.press("enter", "up", "enter")
	h.assertShows("‹ quarantine ›")

	// Esc in a picker keeps the value, and Esc in the dialog closes it.
	h.press("enter", "down", "esc")
	h.assertShows("Apply a Template", "‹ quarantine ›")
	h.press("esc")
	h.assertHides("Apply a Template")
}

// dialogSize is the width and height of the dialog box titled title.
func (h *harness) dialogSize(title string) (w, ht int) {
	h.t.Helper()

	lines := strings.Split(h.view(), "\n")
	for i, l := range lines {
		r := []rune(l)

		col := slices.Index(r, '┏')
		if col < 0 || !strings.HasPrefix(string(r[col:]), "┏━ "+title) {
			continue
		}

		w = slices.Index(r[col:], '┓') + 1

		for j := i + 1; j < len(lines); j++ {
			if rj := []rune(lines[j]); col < len(rj) && rj[col] == '┗' {
				return w, j - i + 1
			}
		}
	}

	h.t.Fatalf("no dialog %q:\n%s", title, h.view())

	return 0, 0
}

func TestTheTemplateDialogKeepsOneSize(t *testing.T) {
	h := openValidatedShowing(t, mailZones(), "v=spf1 mx -all")
	h.send(tea.WindowSizeMsg{Width: 160, Height: 40})

	const title = "Apply a Template to work.example"

	h.press("t")
	w, ht := h.dialogSize(title)

	assertSize := func(step string) {
		t.Helper()

		if gw, gh := h.dialogSize(title); gw != w || gh != ht {
			t.Fatalf("after %s the dialog is %dx%d, want %dx%d:\n%s", step, gw, gh, w, ht, h.view())
		}
	}

	h.press("right")
	assertSize("picking Proton mail")
	h.press("down")
	assertSize("moving to a parameter with a hint")
	h.typeText(strings.Repeat("x", 60))
	assertSize("typing a long value")
	h.press("right")
	assertSize("picking Parked domain")
	h.press("down")
	assertSize("moving to Save")
}
