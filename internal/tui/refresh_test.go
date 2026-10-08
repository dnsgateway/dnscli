package tui_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
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
func refreshZones() *fakeProvider {
	return newFake().
		AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial}, liveRecords...).
		AddZone(dns.Zone{Name: "work.test", ProviderID: "zw2"})
}

// newRefreshHarness starts an unsealed session with cloudflare.work, whose
// Zones were discovered from f, and work.example's Records cached from an
// older Refresh. The cursor is on work.example.
func newRefreshHarness(t *testing.T, f *fakeProvider) (*harness, *store.Store, string) {
	t.Helper()

	return newRefreshHarnessWith(t, f, tui.Deps{})
}

// newRefreshHarnessWith is newRefreshHarness with extra dependencies;
// UnsealMethod is filled in, and Providers unless given.
func newRefreshHarnessWith(t *testing.T, f *fakeProvider, deps tui.Deps) (*harness, *store.Store, string) {
	t.Helper()

	if deps.Providers == nil {
		deps.Providers = f.registry()
	}
	deps.UnsealMethod = fastPassphrase()

	s, path := openStore(t)
	h := newHarnessWith(t, s, deps)
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
func recordsCalls(f *fakeProvider, zoneProviderID string) int {
	n := 0

	for _, c := range f.Calls() {
		if c.Op == providertest.OpRecords && c.Zone.ID == zoneProviderID {
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

func TestAFailedRefreshKeepsTheCacheAndTheRetryKeyRefreshesAgain(t *testing.T) {
	f := refreshZones()
	h, s, _ := newRefreshHarness(t, f)

	f.Fail(providertest.OpRecords, errors.New("connection reset by peer"))
	h.press("enter")

	h.assertShows("203.0.113.9", "Refresh failed (r retries): connection reset by peer")
	h.assertHides("validated this session", "refreshing…")
	h.assertMarker("work.example", "✗")
	h.assertDimmed("work.example")
	h.assertDimmed("203.0.113.9")

	if got := cachedRecordValues(t, s, "work.example"); !slices.Equal(got, []string{"203.0.113.9"}) {
		t.Fatalf("cached records = %q, want the old cache kept", got)
	}

	if z := cachedZone(t, s, "work.example"); z.Serial != 0 || !z.RefreshedAt.IsZero() {
		t.Fatalf("cached zone = %+v, want no serial and no Refresh time", z)
	}

	f.Fail(providertest.OpRecords, nil)
	h.press("r")

	h.assertShows("203.0.113.20", "validated this session")
	h.assertHides("Refresh failed")
	h.assertMarker("work.example", "✓")

	if n := recordsCalls(f, "zw1"); n != 2 {
		t.Fatalf("Records calls = %d, want 2", n)
	}
}

func TestARateLimitedRefreshShowsWhenToRetryAndIsNotRetriedOnItsOwn(t *testing.T) {
	f := refreshZones()
	h, _, _ := newRefreshHarness(t, f)

	f.Fail(providertest.OpRecords, &dnsprovider.APIError{Provider: "cloudflare", StatusCode: http.StatusTooManyRequests, RetryAfter: 90 * time.Second})
	h.press("enter")

	retryAt := testNow.Add(90 * time.Second).Local().Format(time.TimeOnly)
	h.assertShows("rate limited", "retry at "+retryAt, "203.0.113.9")
	h.assertMarker("work.example", "✗")

	// Moving around never retries; only the user does.
	h.press("j", "k", "shift+tab", "tab")

	if n := recordsCalls(f, "zw1"); n != 1 {
		t.Fatalf("Records calls = %d, want 1: a rate-limited Refresh must not be retried automatically", n)
	}
}

func TestARateLimitedZoneIsNotRefreshedUntilTheProviderSaid(t *testing.T) {
	f := refreshZones()
	now := testNow
	h, _, _ := newRefreshHarnessWith(t, f, tui.Deps{Clock: func() time.Time { return now }})

	f.Fail(providertest.OpRecords, &dnsprovider.APIError{Provider: "cloudflare", StatusCode: http.StatusTooManyRequests, RetryAfter: 90 * time.Second})
	h.press("enter")
	f.Fail(providertest.OpRecords, nil)

	retryAt := testNow.Add(90 * time.Second).Local().Format(time.TimeOnly)

	// Opening the Zone again, or retrying, before then does not call the Provider.
	h.press("enter")
	h.press("r")

	h.assertShows("rate-limited until " + retryAt)

	if n := recordsCalls(f, "zw1"); n != 1 {
		t.Fatalf("Records calls = %d before the Provider's retry time, want 1", n)
	}

	now = testNow.Add(90 * time.Second)
	h.press("r")

	h.assertShows("validated this session", "203.0.113.20")

	if n := recordsCalls(f, "zw1"); n != 2 {
		t.Fatalf("Records calls = %d after the Provider's retry time, want 2", n)
	}
}

func TestARefreshWithoutASerialKeepsTheCachedOne(t *testing.T) {
	f := refreshZones()
	h, s, _ := newRefreshHarness(t, f)

	// work.test has a cached serial, but its Provider reports none.
	workTest := cachedZone(t, s, "work.test")
	workTest.Serial = 2026100702
	mustZone(t, s, workTest)

	h.press("j", "enter")
	h.assertShows("validated this session")

	if z := cachedZone(t, s, "work.test"); z.Serial != 2026100702 || !z.RefreshedAt.Equal(testNow) {
		t.Fatalf("cached zone = %+v, want serial 2026100702 kept, refreshed at %v", z, testNow)
	}
}

func TestRetryingAZoneThatCannotBeRefreshedSaysWhy(t *testing.T) {
	f := refreshZones()
	h, _, _ := newRefreshHarness(t, f)

	refresh := h.step(h.pressHolding("enter"))
	h.press("r")
	h.assertShows("work.example is already being Refreshed")
	h.run(refresh)

	// A later session whose build lacks the Account's Provider type.
	s, _ := enrollFirstAccount(t)
	later := newHarnessWith(t, s, tui.Deps{UnsealMethod: fastPassphrase()})
	later.press("enter", "r")

	later.assertShows(`Provider type "cloudflare" is not available in this build`)
	later.assertHides("Passphrase")
}

func TestRefreshesOfOneZoneAreSingleFlight(t *testing.T) {
	f := refreshZones()
	h, s, _ := newRefreshHarness(t, f)

	refresh := h.step(h.pressHolding("enter"))
	h.assertShows("refreshing…")

	// Retrying, and opening the Zone again, while its Refresh is in flight
	// start no second one.
	h.press("r", "enter")
	h.assertShows("refreshing…", "203.0.113.9")

	if n := recordsCalls(f, "zw1"); n != 0 {
		t.Fatalf("Records calls = %d before the held Refresh ran, want 0", n)
	}

	h.run(refresh)

	h.assertShows("validated this session", "203.0.113.20")

	if n := recordsCalls(f, "zw1"); n != 1 {
		t.Fatalf("Records calls = %d, want 1", n)
	}

	if got := cachedRecordValues(t, s, "work.example"); len(got) != 2 {
		t.Fatalf("cached records = %q", got)
	}

	// Once it has finished, the Zone can be Refreshed again.
	h.press("r")

	if n := recordsCalls(f, "zw1"); n != 2 {
		t.Fatalf("Records calls = %d after another retry, want 2", n)
	}
}

func TestARestartStartsEveryZoneNotValidatedAndSelectingPromptsForUnseal(t *testing.T) {
	f := refreshZones()
	h, s, _ := newRefreshHarness(t, f)
	h.press("enter")
	h.assertShows("validated this session")

	// A new session on the same database: the Refreshed cache is there, but
	// nothing is validated.
	f = refreshZones()
	later := newAccountHarness(t, s, f)

	later.assertShows("sealed · read-only", "work.example")
	if row := strings.TrimSpace(later.domainRow("work.example")); row != "›work.example" {
		t.Fatalf("work.example row = %q, want no marker", row)
	}

	later.assertDimmed("work.example")

	later.press("enter")

	// The cached Records show behind the Unseal prompt, which the Refresh needs.
	later.assertShows("203.0.113.20", "Unseal", "needs your Account secrets")
	later.assertHides("validated this session")

	if n := recordsCalls(f, "zw1"); n != 0 {
		t.Fatalf("Records calls = %d while sealed, want 0", n)
	}

	later.typeText(passphrase)
	later.press("enter")

	later.assertShows("unsealed", "validated this session")
	later.assertMarker("work.example", "✓")

	if n := recordsCalls(f, "zw1"); n != 1 {
		t.Fatalf("Records calls = %d after Unseal, want 1", n)
	}
}

func TestARefreshThatNeedsUnsealWaitsForThePromptAlreadyOpen(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := refreshZones()
	h := newAccountHarness(t, s, f)

	// The Zone's cached Records load while the user asks to remove the
	// Account, which opens the Unseal prompt first.
	load := h.pressHolding("enter")
	h.press("shift+tab", "d")
	h.assertShows("Unseal")
	h.run(load)

	h.unsealWith(passphrase)

	h.assertShows("Remove Account", "cloudflare.work")

	h.press("esc")
	h.assertShows("validated this session")

	if n := recordsCalls(f, "zw1"); n != 1 {
		t.Fatalf("Records calls = %d after Unseal, want 1", n)
	}
}

func TestCancellingUnsealLeavesTheZoneStaleWithItsCache(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := refreshZones()
	h := newAccountHarness(t, s, f)

	h.press("enter", "esc")

	h.assertShows("sealed · read-only", "work.example")
	h.assertHides("Unseal", "refreshing…", "validated this session")
	h.assertDimmed("work.example")

	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("provider calls = %+v, want none", calls)
	}
}

func TestTheSerialCheckHintNeverContradictsAValidatedZone(t *testing.T) {
	f := refreshZones()
	// The resolver lags behind the Provider.
	h, _, _ := newRefreshHarnessWith(t, f, tui.Deps{Resolver: &fakeResolver{serials: map[string]uint32{"work.example": liveSerial - 1}}})
	h.assertMarker("work.example", "!")

	h.press("enter")

	h.assertMarker("work.example", "✓")
	h.assertShows("validated this session")
	h.assertHides("possibly stale", "serial matches")
}

func TestAFailedRefreshOfAValidatedZoneMakesItStaleAgain(t *testing.T) {
	f := refreshZones()
	h, s, _ := newRefreshHarness(t, f)
	h.press("enter")
	h.assertShows("validated this session")

	f.Fail(providertest.OpRecords, &dnsprovider.APIError{Provider: "cloudflare", StatusCode: http.StatusServiceUnavailable})
	h.press("r")

	h.assertShows("Refresh failed", "503", "203.0.113.20")
	h.assertHides("validated this session")
	h.assertMarker("work.example", "✗")
	h.assertDimmed("work.example")

	if got := cachedRecordValues(t, s, "work.example"); len(got) != 2 {
		t.Fatalf("cached records = %q, want the last Refresh kept", got)
	}
}

func TestRefreshesRunWithBoundedConcurrency(t *testing.T) {
	f := refreshZones()
	h, _, _ := newRefreshHarnessWith(t, f, tui.Deps{Refreshes: 1})

	first := h.step(h.pressHolding("enter")) // work.example
	h.press("j", "enter")                    // work.test waits for a free slot

	h.assertMarker("work.example", "↻")
	h.assertMarker("work.test", "↻")

	if n := recordsCalls(f, "zw2"); n != 0 {
		t.Fatalf("work.test Records calls = %d while the limit is taken, want 0", n)
	}

	h.run(first)

	h.assertMarker("work.example", "✓")
	h.assertMarker("work.test", "✓")

	if n := recordsCalls(f, "zw2"); n != 1 {
		t.Fatalf("work.test Records calls = %d, want 1", n)
	}
}
