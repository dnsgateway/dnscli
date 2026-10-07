package tui_test

// Seam 1 harness: the top-level Bubble Tea model, built with injected
// dependencies and a real SQLite Store on a temp file. Tests drive Update
// directly and run the returned commands synchronously.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

var testNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

type harness struct {
	t      *testing.T
	model  tea.Model
	store  *store.Store
	ctx    context.Context
	quit   bool
	dbPath string
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

	h := &harness{t: t, store: s}
	h.model = tui.New(context.Background(), tui.Deps{
		Store: s,
		Clock: func() time.Time { return testNow },
	})

	h.run(h.model.Init())
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})

	return h
}

func (h *harness) send(msg tea.Msg) {
	h.t.Helper()

	m, cmd := h.model.Update(msg)
	h.model = m
	h.run(cmd)
}

// run executes a command and feeds every resulting message back into the model.
func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()

	if cmd == nil {
		return
	}

	switch msg := cmd().(type) {
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

// press sends key presses, named the way Bubble Tea prints them ("tab", "shift+tab", "enter", "j").
func (h *harness) press(keys ...string) {
	h.t.Helper()

	for _, k := range keys {
		h.send(keyMsg(k))
	}
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
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

func (h *harness) assertHides(texts ...string) {
	h.t.Helper()

	v := h.view()
	for _, s := range texts {
		if strings.Contains(v, s) {
			h.t.Fatalf("view unexpectedly shows %q:\n%s", s, v)
		}
	}
}
