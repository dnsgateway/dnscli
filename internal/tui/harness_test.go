package tui_test

// Seam 1 harness: the top-level Bubble Tea model, built with injected
// dependencies and a real SQLite Store on a temp file. Tests drive Update
// directly and run the returned commands synchronously.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

var testNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func TestMain(m *testing.M) {
	// Render styles as ANSI so tests can see dimming.
	lipgloss.SetColorProfile(termenv.ANSI256)
	os.Exit(m.Run())
}

// seedCache fills the Store with a small cached data set:
//
//	cloudflare.personal: example.com (3 Records), two.tv (1 Record)
//	cloudflare.work:     example.org (no Records)
func seedCache(t *testing.T, s *store.Store) {
	t.Helper()

	ctx := context.Background()

	personal := mustAccount(t, s, "cloudflare", "personal")
	work := mustAccount(t, s, "cloudflare", "work")

	exampleCom := mustZone(t, s, dns.Zone{AccountID: personal.ID, Name: "example.com", ProviderID: "z1", Serial: 2026100701, RefreshedAt: testNow.Add(-3 * time.Hour)})
	twoTV := mustZone(t, s, dns.Zone{AccountID: personal.ID, Name: "two.tv", ProviderID: "z2"})
	mustZone(t, s, dns.Zone{AccountID: work.ID, Name: "example.org", ProviderID: "z3"})

	if err := s.ReplaceRecords(ctx, exampleCom.ID, []dns.Record{
		{ProviderID: "r1", Type: "A", Name: "example.com", Value: "192.0.2.10", TTL: 300},
		{ProviderID: "r2", Type: "AAAA", Name: "example.com", Value: "2001:db8::10", TTL: 300},
		{ProviderID: "r3", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 3600, Extras: map[string]string{"proxied": "true"}},
	}); err != nil {
		t.Fatalf("seed records: %v", err)
	}

	if err := s.ReplaceRecords(ctx, twoTV.ID, []dns.Record{
		{ProviderID: "r4", Type: "A", Name: "two.tv", Value: "198.51.100.7", TTL: 120},
	}); err != nil {
		t.Fatalf("seed records: %v", err)
	}
}

func mustAccount(t *testing.T, s *store.Store, provider, name string) dns.Account {
	t.Helper()

	a, err := s.AddAccount(context.Background(), dns.Account{Provider: provider, Name: name})
	if err != nil {
		t.Fatalf("add account: %v", err)
	}

	return a
}

func mustZone(t *testing.T, s *store.Store, z dns.Zone) dns.Zone {
	t.Helper()

	z, _, err := s.PutZone(context.Background(), z)
	if err != nil {
		t.Fatalf("put zone: %v", err)
	}

	return z
}

type harness struct {
	t      *testing.T
	model  tea.Model
	store  *store.Store
	ctx    context.Context
	quit   bool
	dbPath string
	// providers are the Provider types the model was given.
	providers dnsprovider.Registry
	// waits are the waits the model asked its timer for, in order.
	waits []time.Duration
	// clock is the time the model reads, which passTime moves on; nil
	// when the test gave the Clock.
	clock *testClock
	// timers are the waits not ended yet, each holding back the command
	// waiting on it.
	timers []*testTimer
	// parking, set while run runs a command, takes the wait that command
	// waits on.
	parking chan *testTimer
}

// testClock is a Clock a test moves on.
type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

// testTimer is a wait the model asked for, ending at end on the clock.
type testTimer struct {
	end  time.Time
	fire chan time.Time
	// done takes what the command waiting on it returns.
	done chan tea.Msg
}

// openStore opens a Store on a fresh temp file.
func openStore(t *testing.T) (*store.Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dnscli.db")

	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	return s, path
}

// newHarness starts the model on the given Store, as if the app had just been launched.
func newHarness(t *testing.T, s *store.Store) *harness {
	t.Helper()

	return newHarnessWith(t, s, tui.Deps{})
}

// newHarnessWith is newHarness with extra dependencies; Store is filled
// in, and Clock, when unset, is a clock at testNow that only passTime
// moves on.
func newHarnessWith(t *testing.T, s *store.Store, deps tui.Deps) *harness {
	t.Helper()

	var c *testClock
	if deps.Clock == nil {
		c = &testClock{now: testNow}
	}

	return newHarnessAt(t, s, deps, c)
}

// newHarnessAt is newHarnessWith on the clock c, which passTime moves on;
// a nil c leaves the Clock to deps.
func newHarnessAt(t *testing.T, s *store.Store, deps tui.Deps, c *testClock) *harness {
	t.Helper()

	h := newHarnessModel(t, s, deps, c)
	h.run(h.model.Init())
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})

	return h
}

// newHarnessModel builds the model for newHarnessAt without starting it.
// After, when unset, records each wait, and ends it once passTime moves
// the clock to its end; a command a test runs itself, such as one held
// back, ends its wait at once. So a test steps through timed work without
// sleeping, and work that waits again and again never runs away.
func newHarnessModel(t *testing.T, s *store.Store, deps tui.Deps, c *testClock) *harness {
	t.Helper()

	deps.Store = s
	if c != nil {
		deps.Clock = c.Now
	}

	h := &harness{t: t, store: s, providers: deps.Providers, clock: c}

	if deps.After == nil {
		clock := deps.Clock
		deps.After = func(d time.Duration) <-chan time.Time {
			h.waits = append(h.waits, d)

			w := &testTimer{end: clock().Add(d), fire: make(chan time.Time, 1)}
			if h.parking == nil {
				w.fire <- w.end
			} else {
				h.parking <- w
			}

			return w.fire
		}
	}

	h.model = tui.New(context.Background(), deps)

	return h
}

func (h *harness) send(msg tea.Msg) {
	h.t.Helper()

	m, cmd := h.model.Update(msg)
	h.model = m
	h.run(cmd)
}

// run executes a command and feeds every resulting message back into the
// model. A command that waits is held back until its wait ends.
func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()

	if cmd == nil {
		return
	}

	done := make(chan tea.Msg, 1)

	h.parking = make(chan *testTimer, 1)

	go func() { done <- cmd() }()

	h.deliver(h.await(done))
}

// await waits for the command answering on done to return, or to wait.
func (h *harness) await(done chan tea.Msg) tea.Msg {
	defer func() { h.parking = nil }()

	select {
	case msg := <-done:
		return msg
	case w := <-h.parking:
		w.done = done
		h.timers = append(h.timers, w)

		return nil
	}
}

// deliver feeds a command's message back into the model.
func (h *harness) deliver(msg tea.Msg) {
	h.t.Helper()

	switch msg := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	case tea.QuitMsg:
		h.quit = true
	default:
		h.send(msg)
	}
}

// passTime moves the clock on by d, ending each wait as the clock reaches
// its end, in order.
func (h *harness) passTime(d time.Duration) {
	h.t.Helper()

	if h.clock == nil {
		h.t.Fatal("passTime needs the harness's clock")
	}

	end := h.clock.now.Add(d)

	for {
		i := -1

		for j, w := range h.timers {
			if !w.end.After(end) && (i < 0 || w.end.Before(h.timers[i].end)) {
				i = j
			}
		}

		if i < 0 {
			break
		}

		w := h.timers[i]
		h.timers = append(h.timers[:i], h.timers[i+1:]...)

		if w.end.After(h.clock.now) {
			h.clock.now = w.end
		}

		h.parking = make(chan *testTimer, 1)
		w.fire <- w.end
		h.deliver(h.await(w.done))
	}

	h.clock.now = end
}

// press sends key presses, named the way Bubble Tea prints them ("tab", "shift+tab", "enter", "j").
func (h *harness) press(keys ...string) {
	h.t.Helper()

	for _, k := range keys {
		h.send(keyMsg(k))
	}
}

// pressHolding sends one key press but holds back the command it returns, so
// a test can see the state while that background work is in flight.
// Pass the result to run to finish the work.
func (h *harness) pressHolding(k string) tea.Cmd {
	h.t.Helper()

	m, cmd := h.model.Update(keyMsg(k))
	h.model = m

	return cmd
}

// keyMsg builds the key message Bubble Tea would deliver for a key name:
// "ctrl+a".."ctrl+z" are real control keys, and "alt+" marks any other key
// as pressed with Alt.
func keyMsg(k string) tea.KeyMsg {
	if rest, ok := strings.CutPrefix(k, "alt+"); ok {
		msg := keyMsg(rest)
		msg.Alt = true

		return msg
	}

	if rest, ok := strings.CutPrefix(k, "ctrl+"); ok && len(rest) == 1 && rest[0] >= 'a' && rest[0] <= 'z' {
		return tea.KeyMsg{Type: tea.KeyCtrlA + tea.KeyType(rest[0]-'a')}
	}

	switch k {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case "insert":
		return tea.KeyMsg{Type: tea.KeyInsert}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

// rawView is the rendered view including ANSI styling.
func (h *harness) rawView() string {
	return h.model.View()
}

// view is the rendered view as plain text.
func (h *harness) view() string {
	return ansi.Strip(h.model.View())
}

func (h *harness) assertShows(texts ...string) {
	h.t.Helper()

	v := h.view()
	for _, s := range texts {
		if !strings.Contains(v, s) {
			h.t.Fatalf("view does not show %q:\n%s", s, v)
		}
	}
}

// assertDimmed checks that text is rendered with the faint (dim) attribute.
func (h *harness) assertDimmed(text string) {
	h.t.Helper()

	if !styledWith(h.rawView(), text, "2") {
		h.t.Fatalf("%q is not dimmed:\n%q", text, h.rawView())
	}
}

// highlighted reports whether text is drawn as the cursor row (reverse video).
func (h *harness) highlighted(text string) bool {
	return styledWith(h.rawView(), text, "7")
}

// styledWith reports whether text, on the first line that contains it, is
// drawn with the given SGR attribute (e.g. "2" for faint, "7" for reverse).
func styledWith(raw, text, attr string) bool {
	for line := range strings.SplitSeq(raw, "\n") {
		before, _, ok := strings.Cut(line, text)
		if !ok {
			continue
		}

		return sgrAttrs(before)[attr]
	}

	return false
}

// sgrAttrs returns the SGR attributes in effect at the end of s. Color
// arguments, such as the 5;7 in 38;5;7, are skipped rather than read as
// attributes.
func sgrAttrs(s string) map[string]bool {
	attrs := map[string]bool{}

	for {
		start := strings.Index(s, "\x1b[")
		if start < 0 {
			return attrs
		}

		s = s[start+2:]

		end := strings.IndexFunc(s, func(r rune) bool { return r >= 0x40 && r <= 0x7e })
		if end < 0 {
			return attrs
		}

		params, final := s[:end], s[end]
		s = s[end+1:]

		if final != 'm' {
			continue // not SGR
		}

		ps := strings.Split(params, ";")
		for j := 0; j < len(ps); j++ {
			switch p := ps[j]; p {
			case "", "0":
				clear(attrs)
			case "38", "48", "58": // extended color: 5;n or 2;r;g;b
				if j+1 < len(ps) && ps[j+1] == "5" {
					j += 2
				} else if j+1 < len(ps) && ps[j+1] == "2" {
					j += 4
				}
			case "22":
				delete(attrs, "1")
				delete(attrs, "2")
			case "27":
				delete(attrs, "7")
			default:
				attrs[p] = true
			}
		}
	}
}

func (h *harness) assertHides(texts ...string) {
	h.t.Helper()

	v := h.view()
	for _, s := range texts {
		if strings.Contains(v, s) {
			h.t.Fatalf("view unexpectedly shows %q:\n%s", s, v)
		}
	}
}
