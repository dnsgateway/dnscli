// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/store"
)

// zonesNamed returns every cached Zone with the zone name, in any Account.
func zonesNamed(t *testing.T, s *store.Store, name string) []dns.Zone {
	t.Helper()

	zones, err := s.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var out []dns.Zone

	for _, z := range zones {
		if z.Name == name {
			out = append(out, z)
		}
	}

	return out
}

// setHostingState caches a Zone's Hosting State, as a listing stores it.
func setHostingState(t *testing.T, s *store.Store, zone string, state dnsprovider.HostingState) {
	t.Helper()

	z := zoneNamed(t, s, zone)
	z.State = state
	mustZone(t, s, z)
}

func TestTheDomainsPaneMarksPendingAndMovedZones(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	setHostingState(t, s, "example.com", dnsprovider.HostingActive)
	setHostingState(t, s, "two.tv", dnsprovider.HostingPending)
	setHostingState(t, s, "example.org", dnsprovider.HostingMoved)

	h := newWideHarness(t, s)

	if row := h.domainRow("two.tv"); !strings.Contains(row, "two.tv · pending") {
		t.Errorf("two.tv row = %q, want it marked pending", row)
	}

	if row := h.domainRow("example.org"); !strings.Contains(row, "example.org · moved") {
		t.Errorf("example.org row = %q, want it marked moved", row)
	}

	// An active Zone looks as it did before Hosting State was known.
	if row := h.domainRow("example.com"); strings.Contains(row, "·") || strings.Contains(row, "active") {
		t.Errorf("example.com row = %q, want no Hosting State mark", row)
	}
}

func TestAZoneWhoseHostingStateIsUnknownIsNotMarked(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)

	h := newWideHarness(t, s)

	for _, zone := range []string{"example.com", "two.tv", "example.org"} {
		if row := h.domainRow(zone); strings.Contains(row, "·") {
			t.Errorf("%s row = %q, want no Hosting State mark", zone, row)
		}
	}
}

func TestTheHostingStateMarkFollowsTheAccountOfASharedZoneName(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	seedDuplicate(t, s)

	for _, z := range zonesNamed(t, s, "example.com") {
		if z.ProviderID == "z4" {
			z.State = dnsprovider.HostingPending
			mustZone(t, s, z)
		}
	}

	h := newWideHarness(t, s)

	// The label is cut short to keep the mark in view.
	if row := h.domainRow("example.com · cloudfla…"); !strings.HasSuffix(strings.TrimRight(row, " "), "cloudfla… · pending") {
		t.Errorf("work row = %q, want its label cut short and marked pending", row)
	}

	if row := h.domainRow("example.com · cloudflare.personal"); strings.Contains(row, "pending") {
		t.Errorf("personal row = %q, want no mark", row)
	}
}

// domainCells returns the Domains pane's cells that mention text.
func (h *harness) domainCells(text string) []string {
	h.t.Helper()

	var out []string

	for line := range strings.SplitSeq(ansi.Strip(h.rawView()), "\n") {
		cells := strings.FieldsFunc(line, func(r rune) bool { return r == '│' || r == '┃' })
		if len(cells) > 1 && strings.Contains(cells[0], text) {
			out = append(out, cells[0])
		}
	}

	return out
}

// At any width, the cursor row included, a Zone's Hosting State mark is
// shown whole, with the label cut short to make room, or not at all when
// the row has no room for it and a label.
func TestTheHostingStateMarkIsShownWholeOrNotAtAllAtAnyWidth(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	setHostingState(t, s, "example.org", dnsprovider.HostingPending)
	setHostingState(t, s, "two.tv", dnsprovider.HostingPending)

	h := newWideHarness(t, s)
	h.press("j", "j") // the cursor is on two.tv, below example.org

	for width := 1; width <= 120; width++ {
		h.send(tea.WindowSizeMsg{Width: width, Height: 30})

		for _, cell := range h.domainCells("pend") {
			if !strings.Contains(cell, " · pending") {
				t.Errorf("width %d: row %q cuts the mark", width, cell)
			}
		}
	}

	// The label is cut, never the mark: on the cursor row and off it.
	for width, want := range map[int][2]string{42: {" e… · pending", "›t… · pending"}, 45: {" ex… · pending", "›tw… · pending"}} {
		h.send(tea.WindowSizeMsg{Width: width, Height: 30})

		if cells := h.domainCells("· pending"); len(cells) != 2 || !strings.HasPrefix(cells[0], want[0]) || !strings.HasPrefix(cells[1], want[1]) {
			t.Errorf("width %d: marked rows = %q, want %q", width, cells, want)
		}
	}

	// Too narrow for the mark and two columns of label: the label alone,
	// cut as usual.
	for width, want := range map[int][2]string{32: {" exampl…", "›two.tv"}, 38: {" example.o…", "›two.tv"}} {
		h.send(tea.WindowSizeMsg{Width: width, Height: 30})

		if cells := h.domainCells("·"); len(cells) != 0 {
			t.Errorf("width %d: rows %q, want no mark", width, cells)
		}

		if org, tv := h.domainCells("ex"), h.domainCells("two"); len(org) != 2 || !strings.HasPrefix(org[1], want[0]) || len(tv) != 1 || !strings.HasPrefix(tv[0], want[1]) {
			t.Errorf("width %d: rows %q %q, want %q", width, org, tv, want)
		}
	}
}
