package tui_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// Seeded cached serials: example.com 2026100701; two.tv and example.org unknown.

// fakeResolver answers SOA queries from a script keyed by zone name. A zone
// with no scripted answer fails like an unreachable resolver.
type fakeResolver struct {
	serials map[string]uint32
	errs    map[string]error
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

	h.press("enter")
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

	h.press("enter")
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

	h.press("enter")
	h.assertShows("resolver serial 2026100702, cached 2026100701: possibly stale")

	// two.tv has no cached serial, so nothing can match it.
	h.press("j", "j", "enter")
	h.assertShows("resolver serial 7, none cached: possibly stale")
}

// newSerialCheckHarness starts a session on the seeded cache with Serial
// Checks against r and a fake Provider, so a test can reload the cache by
// adding an Account.
func newSerialCheckHarness(t *testing.T, s *store.Store, r tui.Resolver) *harness {
	t.Helper()

	return newHarnessWith(t, s, tui.Deps{
		Providers:    provider.Registry{"cloudflare": fake.New().New},
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

	h.press("enter")
	h.assertShows("Serial Check failed: i/o timeout", "192.0.2.10")
}
