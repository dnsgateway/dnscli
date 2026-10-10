package tui_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// Seeded cached serials: example.com 2026100701; two.tv and example.org unknown.

// fakeResolver answers SOA and NS queries from a script keyed by zone name.
// A zone with no scripted answer fails like an unreachable resolver.
type fakeResolver struct {
	serials map[string]uint32
	errs    map[string]error
	// ns are the Delegations; nsErrs fail NS queries.
	ns     map[string][]string
	nsErrs map[string]error

	// soaAt are the serials the Zones' own nameservers answer with, and
	// soaAtErrs fail those queries; see setSOAAt.
	soaAt     map[string]uint32
	soaAtErrs map[string]error

	// ds are the DS records each zone's parent serves, an empty slice
	// for none; dsErrs fail those queries.
	ds     map[string][]string
	dsErrs map[string]error

	mu      sync.Mutex
	nsAsked []string // zone names asked for NS, in order
	atAsked []soaAtQuery
	dsAsked []string // zone names whose parent was asked for DS, in order
}

// setDS scripts the DS records a zone's parent serves; none for no DS.
func (r *fakeResolver) setDS(zone string, ds ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ds == nil {
		r.ds = map[string][]string{}
	}

	r.ds[zone] = append([]string{}, ds...)
	delete(r.dsErrs, zone)
}

// failDS fails the DS queries for a zone with err.
func (r *fakeResolver) failDS(zone string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.dsErrs == nil {
		r.dsErrs = map[string]error{}
	}

	r.dsErrs[zone] = err
}

func (r *fakeResolver) DSAt(_ context.Context, zone string) ([]string, time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.dsAsked = append(r.dsAsked, zone)

	if err, ok := r.dsErrs[zone]; ok {
		return nil, 0, err
	}

	if ds, ok := r.ds[zone]; ok {
		if len(ds) == 0 {
			return nil, 0, nil
		}

		return append([]string(nil), ds...), time.Hour, nil
	}

	return nil, 0, errors.New("no DS answer scripted at the parent of " + zone)
}

// dsQueries returns the zone names whose parent was asked for DS so far.
func (r *fakeResolver) dsQueries() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.dsAsked...)
}

// soaAtQuery is one SOA query sent straight to a Zone's nameservers.
type soaAtQuery struct {
	zone    string
	servers []string
}

// setSOAAt scripts the serial a zone's own nameservers answer with.
func (r *fakeResolver) setSOAAt(zone string, serial uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.soaAt == nil {
		r.soaAt = map[string]uint32{}
	}

	r.soaAt[zone] = serial
}

func (r *fakeResolver) SOAAt(_ context.Context, zone string, servers []string) (uint32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.atAsked = append(r.atAsked, soaAtQuery{zone: zone, servers: append([]string(nil), servers...)})

	if err, ok := r.soaAtErrs[zone]; ok {
		return 0, err
	}

	if serial, ok := r.soaAt[zone]; ok {
		return serial, nil
	}

	return 0, errors.New("no SOA answer scripted at the nameservers of " + zone)
}

// soaAtQueries returns the SOA queries sent straight to Zones' nameservers so far.
func (r *fakeResolver) soaAtQueries() []soaAtQuery {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]soaAtQuery(nil), r.atAsked...)
}

func (r *fakeResolver) NS(_ context.Context, zone string) ([]string, error) {
	r.mu.Lock()
	r.nsAsked = append(r.nsAsked, zone)
	r.mu.Unlock()

	if err, ok := r.nsErrs[zone]; ok {
		return nil, err
	}

	if ns, ok := r.ns[zone]; ok {
		return ns, nil
	}

	return nil, errors.New("no NS answer scripted for " + zone)
}

// nsQueries returns the zone names asked for NS so far.
func (r *fakeResolver) nsQueries() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.nsAsked...)
}

func (r *fakeResolver) SOA(_ context.Context, zone string) (uint32, error) {
	if err, ok := r.errs[zone]; ok {
		return 0, err
	}

	if serial, ok := r.serials[zone]; ok {
		return serial, nil
	}

	return 0, errors.New("no answer scripted for " + zone)
}

// domainRow returns the Domains pane's row for a Zone, as plain text.
func (h *harness) domainRow(zone string) string {
	h.t.Helper()

	for _, line := range strings.Split(ansi.Strip(h.rawView()), "\n") {
		cells := strings.FieldsFunc(line, func(r rune) bool { return r == '│' || r == '┃' })
		if len(cells) > 0 && strings.Contains(cells[0], zone) {
			return cells[0]
		}
	}

	h.t.Fatalf("no Domains row for %q:\n%s", zone, h.view())

	return ""
}

// newHarnessHoldingStartup starts the model like newHarnessWith, but holds
// back the background work the loaded cache starts, so a test can see the
// view while it is in flight. Pass the result to run to finish the work.
func newHarnessHoldingStartup(t *testing.T, s *store.Store, deps tui.Deps) (*harness, tea.Cmd) {
	t.Helper()

	deps.Store = s
	deps.Clock = func() time.Time { return testNow }

	h := &harness{t: t, store: s}
	h.model = tui.New(context.Background(), deps)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})

	m, cmd := h.model.Update(h.model.Init()())
	h.model = m

	return h, cmd
}

func TestStartupRendersTheCacheWhileSerialChecksRunBoundedInTheBackground(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h, checks := newHarnessHoldingStartup(t, s, tui.Deps{
		Resolver:     &fakeResolver{serials: map[string]uint32{"example.com": 2026100701, "two.tv": 1, "example.org": 1}},
		SerialChecks: 2,
	})

	h.assertShows("cloudflare.personal", "example.com", "two.tv", "example.org")

	// Zones are listed example.com, example.org, two.tv: the first two are
	// being checked, and the third waits for a free slot.
	for zone, want := range map[string]string{"example.com": "…", "example.org": "…", "two.tv": ""} {
		row := strings.TrimSpace(h.domainRow(zone))
		if got := strings.TrimSpace(row[strings.Index(row, zone)+len(zone):]); got != want {
			t.Fatalf("%s marker = %q, want %q (row %q)", zone, got, want, row)
		}
	}

	h.press("space")
	h.assertShows("checking the serial…", "192.0.2.10")

	h.run(checks)

	for zone, want := range map[string]string{"example.com": "≈", "example.org": "!", "two.tv": "!"} {
		if row := h.domainRow(zone); !strings.HasSuffix(strings.TrimSpace(row), want) {
			t.Fatalf("%s row = %q, want marker %q", zone, row, want)
		}
	}
}

func TestAMatchingSerialMarksTheZoneHintCurrentButNotValidated(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarnessWith(t, s, tui.Deps{Resolver: &fakeResolver{serials: map[string]uint32{"example.com": 2026100701}}})

	if row := h.domainRow("example.com"); !strings.Contains(row, "≈") {
		t.Fatalf("example.com row does not show the hint-current marker: %q", row)
	}

	h.assertDimmed("example.com")

	h.press("space")
	h.assertShows("serial matches (a hint), not validated")
}

func TestADifferingSerialMarksTheZonePossiblyStale(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarnessWith(t, s, tui.Deps{Resolver: &fakeResolver{serials: map[string]uint32{
		"example.com": 2026100702,
		"two.tv":      7,
		"example.org": 1,
	}}})

	for _, zone := range []string{"example.com", "two.tv"} {
		if row := h.domainRow(zone); !strings.Contains(row, "!") {
			t.Fatalf("%s row does not show the possibly-stale marker: %q", zone, row)
		}
	}

	h.assertDimmed("example.com")

	h.press("space")
	h.assertShows("resolver serial 2026100702, cached 2026100701: possibly stale")

	// two.tv has no cached serial, so nothing can match it.
	h.press("j", "j", "space")
	h.assertShows("resolver serial 7, none cached: possibly stale")
}

// newSerialCheckHarness starts a session on the seeded cache with Serial
// Checks against r and a fake Provider, so a test can reload the cache by
// adding an Account.
func newSerialCheckHarness(t *testing.T, s *store.Store, r tui.Resolver) *harness {
	t.Helper()

	return newHarnessWith(t, s, tui.Deps{
		Providers:    newFake().registry(),
		UnsealMethod: fastPassphrase(),
		Resolver:     r,
	})
}

// reloadCache makes the session reload the cache, as adding an Account does.
func (h *harness) reloadCache() {
	h.t.Helper()

	h.fillAddAccount("lab", "lab-token")
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")
	h.assertShows("cloudflare.lab")
}

func TestAChangedCachedSerialIsComparedAgainOnReload(t *testing.T) {
	s, path := openStore(t)
	seedCache(t, s)

	h := newSerialCheckHarness(t, s, &fakeResolver{serials: map[string]uint32{"example.com": 2026100702}})
	if row := h.domainRow("example.com"); !strings.Contains(row, "!") {
		t.Fatalf("example.com row = %q, want the possibly-stale marker", row)
	}

	// Another session refreshed example.com meanwhile.
	execSQL(t, path, `UPDATE zones SET soa_serial = 2026100702 WHERE name = 'example.com'`)
	h.reloadCache()

	if row := h.domainRow("example.com"); !strings.Contains(row, "≈") {
		t.Fatalf("example.com row = %q, want the hint-current marker for the new cached serial", row)
	}
}

func TestAFailedSerialCheckRunsAgainOnReload(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	r := &fakeResolver{
		serials: map[string]uint32{"example.com": 2026100701},
		errs:    map[string]error{"example.com": errors.New("i/o timeout")},
	}
	h := newSerialCheckHarness(t, s, r)

	if row := h.domainRow("example.com"); !strings.Contains(row, "?") {
		t.Fatalf("example.com row = %q, want the failed-check marker", row)
	}

	// The resolver answers again.
	delete(r.errs, "example.com")
	h.reloadCache()

	if row := h.domainRow("example.com"); !strings.Contains(row, "≈") {
		t.Fatalf("example.com row = %q, want the failed check run again", row)
	}
}

func TestAFailingSerialCheckMarksTheZoneAndShowsTheError(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newHarnessWith(t, s, tui.Deps{Resolver: &fakeResolver{
		serials: map[string]uint32{"example.org": 1},
		errs:    map[string]error{"example.com": errors.New("i/o timeout")},
	}})

	if row := h.domainRow("example.com"); !strings.Contains(row, "?") {
		t.Fatalf("example.com row does not show the failed-check marker: %q", row)
	}

	h.assertDimmed("example.com")

	h.press("space")
	h.assertShows("Serial Check failed: i/o timeout", "192.0.2.10")
}
