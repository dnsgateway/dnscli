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

// statusBar is the Zone Status bar: the first line of the view.
func (h *harness) statusBar() string {
	first, _, _ := strings.Cut(h.view(), "\n")

	return first
}

// statusLine is the status line: the last line of the view.
func (h *harness) statusLine() string {
	v := h.view()

	return v[strings.LastIndex(v, "\n")+1:]
}

func (h *harness) assertStatusLineShows(texts ...string) {
	h.t.Helper()

	line := h.statusLine()
	for _, s := range texts {
		if !strings.Contains(line, s) {
			h.t.Fatalf("status line does not show %q:\n%s", s, line)
		}
	}
}

// Zone Status badge colors, as the SGR codes of the bright ANSI colors.
const (
	green = "92"
	red   = "91"
	blue  = "94"
	gray  = "90"
)

// assertBadges checks that each badge shows in the Zone Status bar in the
// color it maps to.
func (h *harness) assertBadges(want map[string]string) {
	h.t.Helper()

	raw, _, _ := strings.Cut(h.rawView(), "\n")

	for text, color := range want {
		if got := badgeColor(raw, text); got != color {
			h.t.Fatalf("badge %q is color %q, want %q:\n%q", text, got, color, raw)
		}
	}
}

// badgeColor is the SGR code that text is drawn in on line; empty when
// text is not on it.
func badgeColor(line, text string) string {
	// Each badge is styled on its own, so its color is set right before it.
	i := strings.Index(line, "m"+text+"\x1b[")
	if i < 0 {
		return ""
	}

	j := strings.LastIndex(line[:i], "\x1b[")
	if j < 0 {
		return ""
	}

	return line[j+len("\x1b[") : i]
}

func (h *harness) assertStatusBarShows(texts ...string) {
	h.t.Helper()

	line := h.statusBar()
	for _, s := range texts {
		if !strings.Contains(line, s) {
			h.t.Fatalf("Zone Status bar does not show %q:\n%s", s, line)
		}
	}
}

func TestARefreshShowsTheZoneStatusInTheStatusBar(t *testing.T) {
	f := refreshZones()
	f.SetZoneStatus("zw1", dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.Off})
	h, _, _ := newRefreshHarness(t, f)

	h.press("enter")

	h.assertBadges(map[string]string{"MX": green, "SPF": gray, "DNSSEC": green, "HSTS": red})
}

func TestTheZoneStatusBarIsAtTheTopAndTheStatusLineAtTheBottom(t *testing.T) {
	f := refreshZones()
	f.SetZoneStatus("zw1", dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.Off})
	h, _, _ := newRefreshHarness(t, f)

	h.press("enter")

	h.assertStatusBarShows("MX SPF DMARC DKIM · DNSSEC · HSTS ·")
	h.assertStatusLineShows("work.example · cloudflare.work")

	// The session state ends the top row, and the key hints the bottom one.
	if top := h.statusBar(); !strings.HasSuffix(top, "unsealed ") {
		t.Fatalf("top row = %q, want the session state at its right edge", top)
	}

	if bottom := h.statusLine(); !strings.HasSuffix(bottom, "? help · q/Ctrl+C quit ") {
		t.Fatalf("bottom row = %q, want the key hints at its right edge", bottom)
	}

	// A form's keys take the hints' place, outside the form.
	h.press("tab", "i")
	h.assertShows("Add Record")

	if bottom := h.statusLine(); !strings.HasSuffix(bottom, "Esc cancel ") {
		t.Fatalf("bottom row = %q, want the form's keys at its right edge", bottom)
	}

	if n := strings.Count(h.view(), "Esc cancel"); n != 1 {
		t.Fatalf("the form's keys show %d times, want once, in the bottom row:\n%s", n, h.view())
	}
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
	h.assertBadges(map[string]string{"MX": green, "DNSSEC": green, "HSTS": green})
}

func TestAZoneStatusNeverReadIsUnknown(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	h := newHarness(t, s)

	h.press("enter")

	h.assertStatusBarShows("DNSSEC ?", "HSTS ?")
	h.assertBadges(map[string]string{"MX": gray, "SPF": gray, "DMARC": gray, "DKIM": gray, "DNSSEC ?": gray, "HSTS ?": gray})
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
	h.assertStatusBarShows("settings as of 2h ago")
	h.assertBadges(map[string]string{"DNSSEC": green, "HSTS": red})

	if z := cachedZone(t, s, "work.example"); z.Status != (dns.ZoneStatus{DNSSEC: dns.On, HSTS: dns.Off}) || !z.StatusAt.Equal(testNow) {
		t.Fatalf("cached zone = %+v, want the first Zone Status kept", z)
	}

	f.Fail(fake.OpZoneStatus, nil)
	h.press("r")

	h.assertBadges(map[string]string{"DNSSEC": red, "HSTS": green})
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

	h.assertBadges(map[string]string{"MX": gray, "SPF": green, "DMARC": green, "DKIM": green})
}

// openCachedZone opens mail.example, cached with records, in a new session.
func openCachedZone(t *testing.T, records ...dns.Record) *harness {
	t.Helper()

	s, _ := openStore(t)
	a := mustAccount(t, s, "cloudflare", "personal")
	z := mustZone(t, s, dns.Zone{AccountID: a.ID, Name: "mail.example", ProviderID: "zm"})

	if err := s.ReplaceRecords(t.Context(), z.ID, records); err != nil {
		t.Fatalf("cache records: %v", err)
	}

	h := newHarness(t, s)
	h.press("enter")

	return h
}

func TestMXAndSPFRecordsCountOnlyAtTheZoneApex(t *testing.T) {
	h := openCachedZone(t,
		dns.Record{Type: "MX", Name: "eu.mail.example", Value: "10 mx.mail.example", TTL: 300},
		dns.Record{Type: "TXT", Name: "eu.mail.example", Value: `"v=spf1 -all"`, TTL: 300},
		dns.Record{Type: "A", Name: "mail.example", Value: "192.0.2.1", TTL: 300},
	)

	h.assertBadges(map[string]string{"MX": gray, "SPF": gray})
}

func TestANullMXSaysTheZoneTakesNoMail(t *testing.T) {
	h := openCachedZone(t,
		dns.Record{Type: "MX", Name: "mail.example", Value: "0 .", TTL: 300},
		dns.Record{Type: "TXT", Name: "mail.example", Value: `"v=spf1 -all"`, TTL: 300},
	)

	h.assertBadges(map[string]string{"MX": blue, "SPF": blue, "DMARC": gray, "DKIM": gray})
}

func TestAParkedDomainShowsItsMailRecordsAsParked(t *testing.T) {
	h := openCachedZone(t,
		dns.Record{Type: "MX", Name: "mail.example", Value: "0 .", TTL: 300},
		dns.Record{Type: "TXT", Name: "mail.example", Value: `"v=spf1 " "-all"`, TTL: 300},
		dns.Record{Type: "TXT", Name: "_dmarc.mail.example", Value: "v=DMARC1; p=reject; sp=reject; adkim=s; aspf=s", TTL: 300},
		dns.Record{Type: "TXT", Name: "*._domainkey.mail.example", Value: "v=DKIM1; p=", TTL: 300},
	)

	h.assertBadges(map[string]string{"MX": blue, "SPF": blue, "DMARC": blue, "DKIM": blue})
}

func TestARejectingDMARCOfAZoneThatSendsMailIsConfiguredNotParked(t *testing.T) {
	h := openCachedZone(t,
		dns.Record{Type: "MX", Name: "mail.example", Value: "10 mx.mail.example", TTL: 300},
		dns.Record{Type: "TXT", Name: "mail.example", Value: `"v=spf1 mx -all"`, TTL: 300},
		dns.Record{Type: "TXT", Name: "_dmarc.mail.example", Value: "v=DMARC1; p=reject", TTL: 300},
		dns.Record{Type: "TXT", Name: "old._domainkey.mail.example", Value: "v=DKIM1; p=", TTL: 300},
		dns.Record{Type: "CNAME", Name: "s1._domainkey.mail.example", Value: "s1.domainkey.example.net", TTL: 300},
	)

	h.assertBadges(map[string]string{"MX": green, "SPF": green, "DMARC": green, "DKIM": green})
}

func TestHSTSShowsOnlyForCloudflareZones(t *testing.T) {
	s, _ := openStore(t)
	a := mustAccount(t, s, "route53", "personal")
	mustZone(t, s, dns.Zone{AccountID: a.ID, Name: "aws.example", ProviderID: "za"})

	h := newHarness(t, s)
	h.press("enter")

	h.assertStatusBarShows("DNSSEC ?")
	if strings.Contains(h.statusBar(), "HSTS") {
		t.Fatalf("Zone Status bar = %q, want no HSTS outside Cloudflare", h.statusBar())
	}
}

func TestAZoneWhoseDNSSECIsPendingSaysSo(t *testing.T) {
	f := refreshZones()
	f.SetZoneStatus("zw1", dns.ZoneStatus{DNSSEC: dns.Pending, HSTS: dns.On})
	h, s, _ := newRefreshHarness(t, f)
	h.press("enter")

	h.assertStatusBarShows("DNSSEC pending")
	h.assertBadges(map[string]string{"HSTS": green})

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

	h.assertBadges(map[string]string{"DNSSEC": green, "HSTS": red})
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
	h.assertBadges(map[string]string{"DNSSEC": green, "HSTS": green})
}
