package tui_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// withZoneStatus caches a Zone's Zone Status, as a Refresh read it.
func withZoneStatus(t *testing.T, s *store.Store, name string, status dnsprovider.ZoneStatus) dns.Zone {
	t.Helper()

	every, tls := true, status.MinTLS

	z, err := s.SaveZoneStatus(context.Background(), zoneNamed(t, s, name), dnsprovider.ZoneUpdate{DNSSEC: &every, HSTS: &every, MinTLS: &tls}, status, testNow.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}

	return z
}

// settingsFake is the fake with work.example's settings scripted, as a
// ZoneUpdater when updater is set.
func settingsFake(status dnsprovider.ZoneStatus, updater bool) *fakeProvider {
	f := workZones()
	f.SetZoneStatus("zw1", status)
	f.SetZoneUpdater(updater)

	return f
}

// updateZoneCalls are the ZoneUpdates the fake was asked for.
func updateZoneCalls(f *fakeProvider) []providertest.Call {
	var out []providertest.Call

	for _, c := range f.Calls() {
		if c.Op == providertest.OpUpdateZone {
			out = append(out, c)
		}
	}

	return out
}

// The keys that move the Edit Zone dialog's focus from the DNSSEC row,
// where it opens, or from the HSTS or Min TLS row, up past the Account row to the
// Save button, and press it.
var (
	saveFromDNSSEC = []string{"up", "up", "enter"}
	saveFromHSTS   = []string{"up", "up", "up", "enter"}
	saveFromMinTLS = []string{"up", "up", "up", "up", "enter"}
)

// editValidated Unseals the session, Refreshes the highlighted Zone, so it
// is a Validated Zone that may be written (ADR-0001), and opens its Edit
// Zone dialog.
func editValidated(h *harness) {
	h.t.Helper()

	h.press("space")
	h.unsealWith(passphrase)
	h.assertShows("validated this session")
	h.press("left", "e")
}

func TestEnterOnAZoneOpensItsSettings(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	h := newAccountHarness(t, s, settingsFake(status, true))
	h.press("enter")

	h.assertShows("Edit Zone work.example", "The session is sealed")
}

func TestTurningDNSSECOnAsksToConfirmThenCachesThePendingStatusTheProviderReturns(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, true)
	h := newAccountHarness(t, s, f)

	// Sealed, the dialog shows the settings read-only, and says how to
	// change them: a Zone write, which Unseals first.
	h.press("e")
	h.assertShows("Edit Zone work.example", "The session is sealed", "[ Close ]")
	h.assertHides("Passphrase", "‹")

	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("the Provider was called while sealed: %+v", calls)
	}

	h.press("enter")
	h.unsealWith(passphrase)

	// The dialog opens on DNSSEC, showing each setting as it is.
	h.assertShows("Edit Zone work.example", "Account", "cloudflare.work", "‹ off ›", "HSTS", "[ Save ]")

	// Turning DNSSEC on is a Zone write: the Zone is Refreshed first, to
	// be a Validated Zone (ADR-0001).
	h.press("esc", "space")
	h.assertShows("validated this session")
	h.press("left", "e")

	h.press("right")
	h.assertShows("‹ off → on ›")

	h.press(saveFromDNSSEC...)
	h.assertShows("Turn DNSSEC on for work.example at cloudflare.work?")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone before confirming: %+v", calls)
	}

	before := len(f.Calls())

	h.press("enter")

	calls := updateZoneCalls(f)
	if len(calls) != 1 || calls[0].Zone.ID != "zw1" || calls[0].ZoneUpdate.DNSSEC == nil || !*calls[0].ZoneUpdate.DNSSEC || calls[0].ZoneUpdate.HSTS != nil {
		t.Fatalf("UpdateZone calls = %+v, want one turning DNSSEC on for zw1 alone", calls)
	}

	// Cloudflare signs a Zone over time: DNSSEC is pending.
	h.assertShows("DNSSEC of work.example is pending.")
	h.assertHides("Edit Zone work.example")

	want := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Pending, HSTS: dnsprovider.On}
	if got := zoneNamed(t, s, "work.example"); got.Status != want || !got.StatusAt.Equal(testNow) {
		t.Fatalf("cached Zone = %+v, want status %+v read now", got, want)
	}

	// The cache is updated from the answer, without a Refresh.
	for _, c := range f.Calls()[before:] {
		if c.Op == providertest.OpRecords || c.Op == providertest.OpZoneDetails {
			t.Fatalf("a Refresh followed the change: %+v", f.Calls()[before:])
		}
	}
}

func TestZoneSettingsAreNotOfferedWithoutUnsealForAProviderTypeNotDeclaringThem(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.Off}
	withZoneStatus(t, s, "work.example", status)

	h := newAccountHarness(t, s, settingsFake(status, false))

	// The dialog opens read-only, without Unseal, for its Delegation.
	h.press("e")
	h.assertShows("Edit Zone work.example", "cloudflare.work cannot change Zone settings", "Delegation", "[ Close ]")

	// Its settings do not change, nor ask to Unseal.
	h.press("right", "enter")
	h.assertHides("Unseal", "off →")

	h.press("esc")
	h.assertHides("Edit Zone")
}

func TestZoneSettingsAreNotOfferedForAProviderThatCannotChangeThem(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.Off}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, false)
	h := newHarnessWith(t, s, tui.Deps{Providers: f.declaring(dnsprovider.CapZoneUpdater), UnsealMethod: fastPassphrase()})

	h.press("e", "enter")
	h.unsealWith(passphrase)

	// Opened, the Provider cannot: the dialog shows them read-only.
	h.assertShows("Edit Zone work.example", "cloudflare.work cannot change Zone settings", "[ Close ]")
	h.press("right")
	h.assertHides("off → on")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone calls = %+v, want none", calls)
	}
}

func TestEscGoesBackFromConfirmingAZoneSettingThenClosesTheDialogChangingNothing(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.Off}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, true)
	h := newAccountHarness(t, s, f)

	h.press("e", "enter")
	h.unsealWith(passphrase)

	h.press("down", "right")
	h.press(saveFromHSTS...)
	h.assertShows("Turn HSTS on for work.example at cloudflare.work?")

	h.press("esc")
	h.assertShows("Edit Zone work.example", "off → on", "[ Save ]")
	h.assertHides("at cloudflare.work?")

	h.press("esc")
	h.assertHides("Edit Zone work.example")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone calls = %+v, want none", calls)
	}

	if got := zoneNamed(t, s, "work.example"); got.Status != status {
		t.Fatalf("cached status = %+v, want it unchanged", got.Status)
	}
}

func TestAZoneSettingTheProviderRefusesKeepsTheDialogOpenWithTheErrorAndTheCache(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.Off}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, true)
	f.Fail(providertest.OpUpdateZone, &dnsprovider.APIError{Provider: "cloudflare", StatusCode: 403, Messages: []string{"Authentication error"}})

	h := newAccountHarness(t, s, f)

	editValidated(h)
	h.press("right")
	h.press(saveFromDNSSEC...)
	h.press("enter")

	// The form is back as the Zone is cached, with the error, keeping the
	// value picked to try again.
	h.assertShows("Edit Zone work.example", "Could not turn DNSSEC on", "Authentication error", "[ Save ]", "off → on")

	if got := zoneNamed(t, s, "work.example"); got.Status != status {
		t.Fatalf("cached status = %+v, want it unchanged", got.Status)
	}
}

func TestAPendingOrUnknownSettingCanBeTurnedEitherWay(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Pending, HSTS: dnsprovider.Unknown}
	withZoneStatus(t, s, "work.example", status)

	h := newAccountHarness(t, s, settingsFake(status, true))

	h.press("e", "enter")
	h.unsealWith(passphrase)

	h.assertShows("‹ pending ›", "unknown")

	h.press("right")
	h.assertShows("‹ pending → on ›")

	h.press("right")
	h.assertShows("‹ pending → off ›")

	// Round again, the setting is kept as it is.
	h.press("right")
	h.assertShows("‹ pending ›")

	h.press("down", "left")
	h.assertShows("‹ unknown → off ›")
}

func TestHelpOffersEditingTheZoneInAnySessionForAnyAccount(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := onboardFake()
	f.SetZoneUpdater(false)

	h := newAccountHarness(t, s, f)

	// Sealed, and for an Account that cannot change Zone settings: the
	// dialog still shows its Delegation.
	h.press("?")
	h.assertShows("show the Zone's Records", "edit the Zone: its DNSSEC, HSTS or minimum TLS version")
}

func TestZoneSettingsOfAnUpgradedAccountReachingSeveralTenantsAskForOneThenOpen(t *testing.T) {
	f := refreshZones()
	f.SetTenants(twoTenants...)
	f.SetZoneUpdater(true)

	h, s := newUpgradedHarness(t, f)
	old := accountLabeled(t, s, "cloudflare.old")
	mustZone(t, s, dns.Zone{AccountID: old.ID, Name: "old.example", ProviderID: "zw2"})
	h.run(h.model.Init())

	// Show cloudflare.old's Zones, and ask for old.example's settings.
	h.press("space", "tab", "e", "enter")
	h.unsealWith(passphrase)

	h.assertShows("Tenant of cloudflare.old")
	h.assertHides("Edit Zone")
	onlyTenantsListed(t, f)

	// Picked, the Tenant is saved, and the dialog opens.
	h.press("enter")
	h.assertShows("Edit Zone old.example", "DNSSEC")
}

func TestChangingOneZoneSettingKeepsTheOtherAsCachedWhenTheProviderCannotReadIt(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, true)
	h := newAccountHarness(t, s, f)

	editValidated(h)

	// The Provider cannot read HSTS again after turning DNSSEC on.
	f.SetZoneStatus("zw1", dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.Unknown})
	h.press("right")
	h.press(saveFromDNSSEC...)
	h.press("enter")

	h.assertShows("DNSSEC of work.example is pending.")

	want := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Pending, HSTS: dnsprovider.On}
	if got := zoneNamed(t, s, "work.example"); got.Status != want {
		t.Fatalf("cached status = %+v, want %+v", got.Status, want)
	}
}

func TestAZoneSettingChangedButNotCachedClosesTheDialogNamingTheZone(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.Off}
	z := withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, true)
	h := newAccountHarness(t, s, f)

	editValidated(h)
	h.press("right")
	h.press(saveFromDNSSEC...)

	// Another zone takes the name at the Provider meanwhile.
	mustZone(t, s, dns.Zone{AccountID: z.AccountID, Name: "work.example", ProviderID: "zw9"})

	h.press("enter")

	if calls := updateZoneCalls(f); len(calls) != 1 {
		t.Fatalf("UpdateZone calls = %+v, want one", calls)
	}

	h.assertShows("Turn DNSSEC on for work.example was done")
	h.assertHides("Edit Zone", "changing…")

	// The dialog no longer holds the keys.
	h.press("e")
	h.assertShows("Edit Zone work.example")
}

// partialUpdater is a Provider whose UpdateZone writes DNSSEC, then fails.
type partialUpdater struct{ dnsprovider.Provider }

func (partialUpdater) UpdateZone(context.Context, dnsprovider.Zone, dnsprovider.ZoneUpdate) (dnsprovider.ZoneStatus, error) {
	return dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Pending, HSTS: dnsprovider.Unknown}, errors.New("a later write failed")
}

func TestAZoneSettingWrittenBeforeTheProviderFailsIsCachedWithTheError(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	fake := settingsFake(status, true).Factory()
	h := newHarnessWith(t, s, tui.Deps{
		Providers: dnsprovider.Registry{"cloudflare": {Fields: fake.Fields, Traits: dnsprovider.Traits{Capabilities: []dnsprovider.Capability{dnsprovider.CapZoneUpdater}}, New: func(m map[string]string, opts ...dnsprovider.Option) (dnsprovider.Provider, error) {
			p, err := fake.New(m, opts...)

			return partialUpdater{p}, err
		}}},
		UnsealMethod: fastPassphrase(),
	})

	editValidated(h)
	h.press("right")
	h.press(saveFromDNSSEC...)
	h.press("enter")

	// DNSSEC was turned on: the error says so, and what failed after.
	h.assertShows("Edit Zone work.example", "Turn DNSSEC on was done; the Provider then failed: a", "later write failed")

	want := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Pending, HSTS: dnsprovider.On}
	if got := zoneNamed(t, s, "work.example"); got.Status != want || !got.StatusAt.Equal(testNow) {
		t.Fatalf("cached Zone = %+v, want status %+v written now", got, want)
	}

	h.assertShows("DNSSEC       pending")
}

func TestTurningDNSSECOffWithoutCheckingTheDSAtTheParentWarnsOfItAndRefuses(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	// No resolver: whether the parent serves a DS is unknown, never no.
	h := newAccountHarness(t, s, settingsFake(status, true))

	h.press("e", "enter")
	h.unsealWith(passphrase)

	// The hint warns as soon as off is picked; Save then refuses.
	h.press("right")
	h.assertShows("could not tell whether")

	h.press(saveFromDNSSEC...)
	h.assertShows("DNSSEC stays on: dnscli could not tell whether")
	h.assertHides("Turn DNSSEC off for work.example")
}

func TestTurningDNSSECOnFromTheDialogIsRefusedForAStaleZone(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, true)
	h := newAccountHarness(t, s, f)

	// Unsealed, but work.example is not Refreshed this session: turning
	// DNSSEC on is a Zone write, refused as any other (ADR-0001).
	h.press("e", "enter")
	h.unsealWith(passphrase)
	h.press("right")
	h.press(saveFromDNSSEC...)

	h.assertShows("work.example is a Stale Zone")
	h.assertHides("Turn DNSSEC on for work.example at")

	h.press("enter")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone calls = %+v, want none for a Stale Zone", calls)
	}
}

func TestTurningHSTSOnWarnsThatBrowsersWillPinHTTPS(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.Off}
	withZoneStatus(t, s, "work.example", status)

	h := newAccountHarness(t, s, settingsFake(status, true))

	h.press("e", "enter")
	h.unsealWith(passphrase)
	h.press("down", "right")
	h.assertShows("six months")

	h.press(saveFromHSTS...)
	h.assertShows("Turn HSTS on for work.example", "HTTPS", "six months")
}

func TestZoneSettingsOfAProviderTypeWithoutThemAreRefusedWithoutUnseal(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	home := mustAccount(t, s, "namecheap", "home")
	mustZone(t, s, dns.Zone{AccountID: home.ID, Name: "home.example", ProviderID: "home.example"})

	h := newAccountHarness(t, s, workZones())

	// Read-only, with the one setting a Provider other than Cloudflare
	// has: no HSTS nor minimum TLS version.
	h.press("e")

	h.assertShows("Edit Zone home.example", "namecheap.home cannot change Zone settings", "DNSSEC", "Delegation")
	h.assertHides("Unseal", "HSTS", "Min TLS")
}

func TestBothZoneSettingsChangedAreMadeInOneUpdateOnceConfirmed(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, true)
	h := newAccountHarness(t, s, f)

	editValidated(h)
	h.press("right", "down", "right")
	h.press(saveFromHSTS...)

	// The confirmation has a button to press, and says which keys do.
	h.assertShows("Turn DNSSEC on and turn HSTS off for work.example at", "› [ Apply ]", "Enter apply · Esc back")
	h.assertHides("[ Save ]")
	h.press("enter")

	calls := updateZoneCalls(f)
	if len(calls) != 1 || calls[0].ZoneUpdate.DNSSEC == nil || !*calls[0].ZoneUpdate.DNSSEC ||
		calls[0].ZoneUpdate.HSTS == nil || *calls[0].ZoneUpdate.HSTS {
		t.Fatalf("UpdateZone calls = %+v, want one turning DNSSEC on and HSTS off", calls)
	}

	h.assertShows("DNSSEC of work.example is pending; HSTS of work.example is off.")
}

func TestSavingTheEditZoneDialogUnchangedClosesItWithoutAskingTheProvider(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, true)
	h := newAccountHarness(t, s, f)

	h.press("e", "enter")
	h.unsealWith(passphrase)

	// Changed and changed back is unchanged.
	h.press("right", "left")
	h.press(saveFromDNSSEC...)

	h.assertHides("Edit Zone", "Turn DNSSEC")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone calls = %+v, want none", calls)
	}
}

func TestEditZoneHintsSayWhatTheFocusedRowIsForAndTheStatusLineHowToMove(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	withZoneStatus(t, s, "work.example", dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.Off})

	h := newAccountHarness(t, s, settingsFake(dnsprovider.ZoneStatus{}, true))

	h.press("e", "enter")
	h.unsealWith(passphrase)

	h.assertShows("Signs the Zone's Records", "left right change", "Enter list or save", "Esc cancel")

	h.press("down")
	h.assertShows("over HTTPS only")
	h.assertHides("Signs the Zone's Records")

	h.press("down")
	h.assertShows("The oldest TLS version")

	h.press("down")
	h.assertShows("dnscli does not", "change Delegation (ADR-0004)")

	h.press("up", "up", "up", "up")
	h.assertShows("A Zone stays in its")
}

func TestEditZoneShowsTheDelegationTheNameserversExpectedAndWhetherItIsTheLiveZone(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	expectNameservers(t, s, "cloudflare.work", "work.example", "ada.ns.cloudflare.com", "bob.ns.cloudflare.com")

	f := settingsFake(dnsprovider.ZoneStatus{}, true)
	h := newHarnessWith(t, s, tui.Deps{
		Providers:    f.registry(),
		UnsealMethod: fastPassphrase(),
		Resolver: &fakeResolver{ns: map[string][]string{
			"work.example": {"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"},
		}},
	})

	h.press("e", "enter")
	h.unsealWith(passphrase)

	h.assertShows("Delegation   ada.ns.cloudflare.com", "Nameservers  ada.ns.cloudflare.com", "bob.ns.cloudflare.com", "Live Zone    ● yes")

	// Read-only: Left and Right change nothing there, Enter moves on.
	h.press("down", "down", "down", "right", "enter")
	h.assertShows("The nameservers cloudflare.work expects")
}

func TestEditInTheDomainsPaneEditsTheZoneAndInTheAccountsPaneTheAccount(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newAccountHarness(t, s, settingsFake(dnsprovider.ZoneStatus{}, true))

	h.press("e", "enter")
	h.unsealWith(passphrase)
	h.assertShows("Edit Zone work.example")

	// Down past the last Zone is the Accounts pane.
	h.press("esc", "down", "down", "e")
	h.assertShows("Edit Account cloudflare.work")
	h.assertHides("Edit Zone")
}

func TestRaisingTheMinimumTLSVersionWarnsOfOldClientsThenSetsItOnceConfirmed(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.On, MinTLS: dnsprovider.TLS10}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, true)
	h := newAccountHarness(t, s, f)

	h.press("e", "enter")
	h.unsealWith(passphrase)
	h.press("down", "down")
	h.assertShows("Min TLS", "‹ 1.0 ›", "The oldest TLS version")

	// 1.1 is older than most clients' newest: no warning.
	h.press("right")
	h.assertShows("‹ 1.0 → 1.1 ›")
	h.assertHides("Clients that cannot use")

	h.press("right")
	h.assertShows("‹ 1.0 → 1.2 ›", "Clients that cannot use TLS 1.2", "will then fail to reach")

	h.press(saveFromMinTLS...)
	h.assertShows("Set the minimum TLS version to 1.2 for work.example at", "› [ Apply ]")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone before confirming: %+v", calls)
	}

	h.press("enter")

	calls := updateZoneCalls(f)
	if len(calls) != 1 || calls[0].ZoneUpdate.MinTLS == nil || *calls[0].ZoneUpdate.MinTLS != dnsprovider.TLS12 ||
		calls[0].ZoneUpdate.DNSSEC != nil || calls[0].ZoneUpdate.HSTS != nil {
		t.Fatalf("UpdateZone calls = %+v, want one setting TLS 1.2 alone", calls)
	}

	h.assertShows("Min TLS of work.example is 1.2.")

	want := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.On, MinTLS: dnsprovider.TLS12}
	if got := zoneNamed(t, s, "work.example"); got.Status != want {
		t.Fatalf("cached status = %+v, want %+v", got.Status, want)
	}
}

func TestAnUnknownMinimumTLSVersionCanBeSetOrKept(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, true)
	h := newAccountHarness(t, s, f)

	h.press("e", "enter")
	h.unsealWith(passphrase)
	h.press("down", "down")
	h.assertShows("‹ unknown ›")

	// The picker lists keeping it, then every version.
	h.press("enter")
	h.assertShows("unknown", "1.0", "1.1", "1.2", "1.3")

	h.press("down", "down", "down", "down", "enter")
	h.assertShows("‹ unknown → 1.3 ›", "Clients that cannot use TLS 1.3")

	h.press("left", "left", "left", "left")
	h.assertShows("‹ unknown ›")
	h.assertHides("Clients that cannot use")
}

func TestEditZoneSaysWhyItsDelegationAndNameserversAreUnknown(t *testing.T) {
	s, _ := enrollFirstAccount(t)

	f := settingsFake(dnsprovider.ZoneStatus{}, true)
	h := newHarnessWith(t, s, tui.Deps{
		Providers:    f.registry(),
		UnsealMethod: fastPassphrase(),
		Resolver:     &fakeResolver{nsErrs: map[string]error{"work.example": errors.New("i/o timeout")}},
	})

	h.press("e", "enter")
	h.unsealWith(passphrase)
	h.assertShows("Delegation   lookup failed", "Nameservers  not cached", "Live Zone    unknown")

	h.press("down", "down", "down")
	h.assertShows("the lookup", "failed: i/o timeout")

	h.press("down")
	h.assertShows("not cached: re-list the Account (L) to read them")
}

// tlsFailsAfterDNSSEC is a Provider whose UpdateZone turns DNSSEC on, then
// fails to set the minimum TLS version, which it returns unknown.
type tlsFailsAfterDNSSEC struct{ dnsprovider.Provider }

func (tlsFailsAfterDNSSEC) UpdateZone(context.Context, dnsprovider.Zone, dnsprovider.ZoneUpdate) (dnsprovider.ZoneStatus, error) {
	return dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Pending, HSTS: dnsprovider.On}, errors.New("TLS refused")
}

func TestASettingThatFailsAfterAnotherIsMadeKeepsItsCachedValueAndItsPick(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.On, MinTLS: dnsprovider.TLS10}
	withZoneStatus(t, s, "work.example", status)

	fake := settingsFake(status, true).Factory()
	h := newHarnessWith(t, s, tui.Deps{
		Providers: dnsprovider.Registry{"cloudflare": {Fields: fake.Fields, Traits: dnsprovider.Traits{Capabilities: []dnsprovider.Capability{dnsprovider.CapZoneUpdater}}, New: func(m map[string]string, opts ...dnsprovider.Option) (dnsprovider.Provider, error) {
			p, err := fake.New(m, opts...)

			return tlsFailsAfterDNSSEC{p}, err
		}}},
		UnsealMethod: fastPassphrase(),
	})

	editValidated(h)
	h.press("right", "down", "down", "right", "right")
	h.press(saveFromMinTLS...)
	h.press("enter")

	// The error names what was done and what failed, apart.
	h.assertShows("Turn DNSSEC on was done; could not set the minimum TLS", "TLS refused")

	// DNSSEC is cached as written; the minimum TLS version keeps 1.0.
	want := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Pending, HSTS: dnsprovider.On, MinTLS: dnsprovider.TLS10}
	if got := zoneNamed(t, s, "work.example"); got.Status != want {
		t.Fatalf("cached status = %+v, want %+v", got.Status, want)
	}

	// The 1.2 picked is kept to try again; DNSSEC is no longer a change.
	h.assertShows("DNSSEC       pending", "1.0 → 1.2")
}

func TestASealedSessionShowsTheDelegationWithoutUnseal(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	expectNameservers(t, s, "cloudflare.work", "work.example", "ada.ns.cloudflare.com", "bob.ns.cloudflare.com")

	f := settingsFake(dnsprovider.ZoneStatus{}, true)
	h := newHarnessWith(t, s, tui.Deps{
		Providers:    f.registry(),
		UnsealMethod: fastPassphrase(),
		Resolver:     &fakeResolver{ns: map[string][]string{"work.example": {"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}}},
	})

	h.press("e")
	h.assertShows("Edit Zone work.example", "Delegation   ada.ns.cloudflare.com", "Live Zone    ● yes", "↑/k ↓/j field · Esc close")
	h.assertHides("Passphrase")

	// Close leaves the session sealed, the Provider never opened.
	h.press("up", "up", "enter")
	h.assertHides("Edit Zone", "Passphrase")

	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("Provider calls = %+v, want none", calls)
	}
}
