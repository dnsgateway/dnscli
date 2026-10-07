package tui_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
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

func TestAZoneWhoseDNSSECIsPendingSaysSo(t *testing.T) {
	f := refreshZones()
	f.SetZoneStatus("zw1", dns.ZoneStatus{DNSSEC: dns.Pending, HSTS: dns.On})
	h, s, _ := newRefreshHarness(t, f)
	h.press("enter")

	h.assertStatusBarShows("DNSSEC pending", "HSTS on")

	// From the cache, too.
	h = newHarness(t, s)
	h.press("enter")
	h.assertStatusBarShows("DNSSEC pending")
}

func TestARateLimitedZoneStatusReadStillSavesTheRecordsButHoldsTheNextRefresh(t *testing.T) {
	f := refreshZones()
	f.SetZoneStatus("zw1", dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.Off})
	now := testNow
	h, _, _ := newRefreshHarnessWith(t, f, tui.Deps{Clock: func() time.Time { return now }})

	f.Fail(fake.OpZoneStatus, &provider.APIError{Provider: "cloudflare", StatusCode: http.StatusTooManyRequests, RetryAfter: 90 * time.Second})
	h.press("enter")

	h.assertShows("validated this session", "203.0.113.20")
	h.assertStatusBarShows("DNSSEC ?", "HSTS ?")

	// The Provider asked not to be called again yet.
	f.Fail(fake.OpZoneStatus, nil)
	h.press("r")

	retryAt := testNow.Add(90 * time.Second).Local().Format(time.TimeOnly)
	h.assertShows("rate-limited until " + retryAt)

	if n := recordsCalls(f, "zw1"); n != 1 {
		t.Fatalf("Records calls = %d before the Provider's retry time, want 1", n)
	}

	now = testNow.Add(90 * time.Second)
	h.press("r")

	h.assertStatusBarShows("DNSSEC on", "HSTS off")
}

// statusFirst is a Provider whose Records answer only once its Zone Status
// is being read, so a Refresh that reads them one after the other fails.
type statusFirst struct {
	provider.Provider

	statusAsked chan struct{}
}

func (p *statusFirst) ZoneStatus(ctx context.Context, z dns.Zone) (dns.ZoneStatus, error) {
	close(p.statusAsked)

	return p.Provider.ZoneStatus(ctx, z)
}

func (p *statusFirst) Records(ctx context.Context, z dns.Zone) (provider.RecordSet, error) {
	select {
	case <-p.statusAsked:
		return p.Provider.Records(ctx, z)
	case <-time.After(5 * time.Second):
		return provider.RecordSet{}, errors.New("the Zone Status was not read alongside the Records")
	}
}

func TestARefreshReadsTheZoneStatusAlongsideTheRecords(t *testing.T) {
	f := refreshZones()
	f.SetZoneStatus("zw1", dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.On})

	s, _ := openStore(t)
	h := newHarnessWith(t, s, tui.Deps{
		Providers: provider.Registry{"cloudflare": func(a dns.Account, token string) (provider.Provider, error) {
			p, err := f.New(a, token)

			return &statusFirst{Provider: p, statusAsked: make(chan struct{})}, err
		}},
		UnsealMethod: fastPassphrase(),
	})
	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")

	h.press("enter")

	h.assertShows("validated this session", "203.0.113.20")
	h.assertStatusBarShows("DNSSEC on", "HSTS on")
}
