package tui_test

import (
	"strings"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
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
