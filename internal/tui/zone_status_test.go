package tui_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// statusBar is the status bar: the last two lines of the view.
func (h *harness) statusBar() string {
	lines := strings.Split(h.view(), "\n")

	return strings.Join(lines[max(len(lines)-2, 0):], "\n")
}

func (h *harness) assertStatusBarShows(texts ...string) {
	h.t.Helper()

	line := h.statusBar()
	for _, s := range texts {
		if !strings.Contains(line, s) {
			h.t.Fatalf("status bar does not show %q:\n%s", s, line)
		}
	}
}

func TestARefreshShowsTheZoneStatusInTheStatusBar(t *testing.T) {
	f := refreshZones()
	f.SetZoneStatus("zw1", dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.Off})
	h, _, _ := newRefreshHarness(t, f)

	h.press("enter")

	h.assertStatusBarShows("mail: MX", "DNSSEC on", "HSTS off")
}

func TestTheCachedZoneStatusShowsBeforeAnyRefreshAndOffline(t *testing.T) {
	f := refreshZones()
	f.SetZoneStatus("zw1", dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.On})
	h, s, _ := newRefreshHarness(t, f)
	h.press("enter")

	// A later session, with no Provider to Refresh from.
	h = newHarness(t, s)
	h.press("enter")

	h.assertShows("cached, last Refresh just now")
	h.assertStatusBarShows("mail: MX", "DNSSEC on", "HSTS on")
}

func TestAZoneStatusNeverReadIsUnknown(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	h := newHarness(t, s)

	h.press("enter")

	h.assertStatusBarShows("mail: none", "DNSSEC ?", "HSTS ?")
}

func TestAFailedZoneStatusReadKeepsTheCachedOneAndSaysHowOldItIs(t *testing.T) {
	f := refreshZones()
	f.SetZoneStatus("zw1", dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.Off})
	now := testNow
	h, s, _ := newRefreshHarnessWith(t, f, tui.Deps{Clock: func() time.Time { return now }})
	h.press("enter")

	now = testNow.Add(2 * time.Hour)
	f.SetZoneStatus("zw1", dns.ZoneStatus{DNSSEC: dns.Off, HSTS: dns.On})
	f.Fail(fake.OpZoneStatus, errors.New("connection reset by peer"))
	h.press("r")

	h.assertShows("validated this session", "last Refresh just now")
	h.assertStatusBarShows("DNSSEC on", "HSTS off", "settings as of 2h ago")

	if z := cachedZone(t, s, "work.example"); z.Status != (dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.Off}) || !z.StatusAt.Equal(testNow) {
		t.Fatalf("cached zone = %+v, want the first Zone Status kept", z)
	}

	f.Fail(fake.OpZoneStatus, nil)
	h.press("r")

	h.assertStatusBarShows("DNSSEC off", "HSTS on")
	h.assertHides("settings as of")
}

func TestTheStatusBarSaysWhichMailRecordsTheCachedZoneHas(t *testing.T) {
	s, _ := openStore(t)
	a := mustAccount(t, s, "cloudflare", "personal")
	z := mustZone(t, s, dns.Zone{AccountID: a.ID, Name: "mail.example", ProviderID: "zm"})

	if err := s.ReplaceRecords(t.Context(), z.ID, []dns.Record{
		{Type: "TXT", Name: "mail.example", Value: `"v=spf1 include:_spf.example.net -all"`, TTL: 300},
		{Type: "TXT", Name: "_dmarc.mail.example", Value: "v=DMARC1; p=reject", TTL: 300},
		{Type: "TXT", Name: "s1._domainkey.mail.example", Value: "v=DKIM1; k=rsa; p=MIGf", TTL: 300},
		{Type: "TXT", Name: "mail.example", Value: "google-site-verification=abc", TTL: 300},
	}); err != nil {
		t.Fatalf("cache records: %v", err)
	}

	h := newHarness(t, s)
	h.press("enter")

	h.assertStatusBarShows("mail: SPF DMARC DKIM ·")
	h.assertHides("MX")
}
