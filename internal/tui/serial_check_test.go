package tui_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

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
