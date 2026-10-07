package tui_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
	"github.com/sergelogvinov/dnscli/internal/store"
)

// Records of work.example as the Provider has them now, and as an older
// session cached them.
var (
	liveRecords = []dns.Record{
		{ProviderID: "rw1", Type: "A", Name: "work.example", Value: "203.0.113.20", TTL: 300},
		{ProviderID: "rw2", Type: "MX", Name: "work.example", Value: "10 mail.work.example", TTL: 3600},
	}
	cachedRecords = []dns.Record{
		{ProviderID: "rw1", Type: "A", Name: "work.example", Value: "203.0.113.9", TTL: 300},
	}
)

const liveSerial = 2026100705

// refreshZones scripts the fake Provider with work.example, holding
// liveRecords at liveSerial, and an empty work.test.
func refreshZones() *fake.Provider {
	return fake.New().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial}, liveRecords...).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2"})
}

// newRefreshHarness starts an unsealed session with cloudflare.work, whose
// Zones were discovered from f, and work.example's Records cached from an
// older Refresh. The cursor is on work.example.
func newRefreshHarness(t *testing.T, f *fake.Provider) (*harness, *store.Store, string) {
	t.Helper()

	s, path := openStore(t)
	h := newAccountHarness(t, s, f)
	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")
	h.assertShows("work.example", "unsealed")

	if err := s.ReplaceRecords(context.Background(), zoneID(t, s, "work.example"), cachedRecords); err != nil {
		t.Fatalf("cache records: %v", err)
	}

	return h, s, path
}

func zoneID(t *testing.T, s *store.Store, name string) int64 {
	t.Helper()

	return cachedZone(t, s, name).ID
}

func cachedZone(t *testing.T, s *store.Store, name string) dns.Zone {
	t.Helper()

	zones, err := s.Zones(context.Background())
	if err != nil {
		t.Fatalf("list zones: %v", err)
	}

	i := slices.IndexFunc(zones, func(z dns.Zone) bool { return z.Name == name })
	if i < 0 {
		t.Fatalf("zone %s is not cached: %+v", name, zones)
	}

	return zones[i]
}

func cachedRecordValues(t *testing.T, s *store.Store, name string) []string {
	t.Helper()

	records, err := s.Records(context.Background(), zoneID(t, s, name))
	if err != nil {
		t.Fatalf("list records: %v", err)
	}

	var values []string
	for _, r := range records {
		values = append(values, r.Value)
	}

	return values
}

// recordsCalls counts the Provider's Records calls for a Zone.
func recordsCalls(f *fake.Provider, zoneProviderID string) int {
	n := 0

	for _, c := range f.Calls() {
		if c.Op == fake.OpRecords && c.Zone.ProviderID == zoneProviderID {
			n++
		}
	}

	return n
}

// step runs cmd, delivers its message, and holds back the command the
// model returns for it, so a test can see the state in between.
func (h *harness) step(cmd tea.Cmd) tea.Cmd {
	h.t.Helper()

	if cmd == nil {
		h.t.Fatal("no command to step")
	}

	m, next := h.model.Update(cmd())
	h.model = m

	return next
}

func (h *harness) assertMarker(zone, want string) {
	h.t.Helper()

	if row := strings.TrimSpace(h.domainRow(zone)); !strings.HasSuffix(row, want) {
		h.t.Fatalf("%s row = %q, want marker %q", zone, row, want)
	}
}

func (h *harness) assertNotDimmed(text string) {
	h.t.Helper()

	if styledWith(h.rawView(), text, "2") {
		h.t.Fatalf("%q is dimmed:\n%q", text, h.rawView())
	}
}

func TestSelectingAZoneRefreshesItInTheBackgroundAndValidatesIt(t *testing.T) {
	f := refreshZones()
	h, s, _ := newRefreshHarness(t, f)

	// The cached Records show at once, while the Refresh is in flight.
	refresh := h.step(h.pressHolding("enter"))

	h.assertShows("203.0.113.9", "refreshing…")
	h.assertHides("203.0.113.20")
	h.assertMarker("work.example", "↻")
	h.assertDimmed("work.example")

	h.run(refresh)

	h.assertShows("203.0.113.20", "10 mail.work.example", "validated this session", "last Refresh just now")
	h.assertHides("203.0.113.9", "refreshing…")
	h.assertMarker("work.example", "✓")
	h.assertNotDimmed("work.example")
	h.assertNotDimmed("203.0.113.20")

	if got := cachedRecordValues(t, s, "work.example"); !slices.Equal(got, []string{"203.0.113.20", "10 mail.work.example"}) {
		t.Fatalf("cached records = %q", got)
	}

	if z := cachedZone(t, s, "work.example"); z.Serial != liveSerial || !z.RefreshedAt.Equal(testNow) {
		t.Fatalf("cached zone = %+v, want serial %d refreshed at %v", z, liveSerial, testNow)
	}

	if n := recordsCalls(f, "zw1"); n != 1 {
		t.Fatalf("Records calls = %d, want 1", n)
	}
}
