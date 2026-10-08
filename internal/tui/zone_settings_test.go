package tui_test

import (
	"context"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
)

// withZoneStatus caches a Zone's Zone Status, as a Refresh read it.
func withZoneStatus(t *testing.T, s *store.Store, name string, status dnsprovider.ZoneStatus) dns.Zone {
	t.Helper()

	z, err := s.SaveZoneStatus(context.Background(), zoneNamed(t, s, name), status, testNow.Add(-1))
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

func TestTurningDNSSECOnAsksToConfirmThenCachesTheStatusTheProviderReturns(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.On}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, true)
	h := newAccountHarness(t, s, f)

	// Zone settings are a Zone write: the sealed session Unseals first.
	h.press("S")

	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("the Provider was called while sealed: %+v", calls)
	}

	h.unsealWith(passphrase)

	h.assertShows("Zone settings of work.example", "Turn DNSSEC on", "Turn HSTS off")
	h.assertHides("Turn DNSSEC off", "Turn HSTS on")

	h.press("enter")
	h.assertShows("Turn DNSSEC on for work.example at cloudflare.work?")

	if calls := updateZoneCalls(f); len(calls) != 0 {
		t.Fatalf("UpdateZone before confirming: %+v", calls)
	}

	h.press("enter")

	calls := updateZoneCalls(f)
	if len(calls) != 1 || calls[0].Zone.ID != "zw1" || calls[0].ZoneUpdate.DNSSEC == nil || !*calls[0].ZoneUpdate.DNSSEC || calls[0].ZoneUpdate.HSTS != nil {
		t.Fatalf("UpdateZone calls = %+v, want one turning DNSSEC on for zw1 alone", calls)
	}

	h.assertShows("DNSSEC of work.example is on.")
	h.assertHides("Zone settings of work.example")

	want := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.On, HSTS: dnsprovider.On}
	if got := zoneNamed(t, s, "work.example"); got.Status != want || !got.StatusAt.Equal(testNow) {
		t.Fatalf("cached Zone = %+v, want status %+v read now", got, want)
	}

	// The cache is updated from the answer, without a Refresh.
	for _, c := range f.Calls() {
		if c.Op == providertest.OpRecords || c.Op == providertest.OpZoneStatus {
			t.Fatalf("a Refresh followed the change: %+v", f.Calls())
		}
	}
}

func TestZoneSettingsAreNotOfferedForAProviderThatCannotChangeThem(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Off, HSTS: dnsprovider.Off}
	withZoneStatus(t, s, "work.example", status)

	f := settingsFake(status, false)
	h := newAccountHarness(t, s, f)

	h.press("S")
	h.unsealWith(passphrase)

	h.assertShows("cloudflare.work cannot change Zone settings")
	h.assertHides("Zone settings of work.example", "Turn DNSSEC on")

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

	h.press("S")
	h.unsealWith(passphrase)
	h.assertShows("Turn DNSSEC off", "Turn HSTS on")

	h.press("j", "enter")
	h.assertShows("Turn HSTS on for work.example at cloudflare.work?")

	h.press("esc")
	h.assertShows("Zone settings of work.example", "Turn DNSSEC off", "Turn HSTS on")
	h.assertHides("at cloudflare.work?")

	h.press("esc")
	h.assertHides("Zone settings of work.example")

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

	h.press("S")
	h.unsealWith(passphrase)
	h.press("enter", "enter")

	h.assertShows("Zone settings of work.example", "Could not turn DNSSEC on", "Turn DNSSEC on")

	if got := zoneNamed(t, s, "work.example"); got.Status != status {
		t.Fatalf("cached status = %+v, want it unchanged", got.Status)
	}
}

func TestAPendingOrUnknownSettingCanBeTurnedEitherWay(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	status := dnsprovider.ZoneStatus{DNSSEC: dnsprovider.Pending, HSTS: dnsprovider.Unknown}
	withZoneStatus(t, s, "work.example", status)

	h := newAccountHarness(t, s, settingsFake(status, true))

	h.press("S")
	h.unsealWith(passphrase)

	h.assertShows("DNSSEC pending · HSTS unknown", "Turn DNSSEC on", "Turn DNSSEC off", "Turn HSTS on", "Turn HSTS off")
}

func TestZoneSettingsActOnTheDomainsPanesZoneOnly(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := settingsFake(dnsprovider.ZoneStatus{}, true)
	h := newAccountHarness(t, s, f)

	h.press("shift+tab", "S")

	h.assertShows("Highlight a Zone in the Domains pane first.")
	h.assertHides("Unseal", "Zone settings of")
}

func TestHelpOffersZoneSettingsInTheDomainsPane(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newAccountHarness(t, s, workZones())

	h.press("?")
	h.assertShows("turn the Zone's DNSSEC or HSTS on or off")
}
