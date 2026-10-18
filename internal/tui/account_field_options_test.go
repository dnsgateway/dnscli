// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui_test

import (
	"maps"
	"testing"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// optionFields are credential fields of each kind the Account form
// handles: a secret token, a typed region with a default, and an egress
// picked from choices, with a default.
func optionFields() []dnsprovider.Field {
	return []dnsprovider.Field{
		{Name: "token", Label: "API token", Secret: true},
		{Name: "region", Label: "Region", Default: "eu-west"},
		{Name: "egress", Label: "Egress", Default: "auto", Choices: []string{"auto", "gateway", "agent"}},
	}
}

// optionsRegistry registers f as "dnsgate", taking fields; fb keeps the
// field map of each build.
func optionsRegistry(f *fakeProvider, fb *fieldBuilds, fields []dnsprovider.Field) dnsprovider.Registry {
	inner := f.Factory()

	return dnsprovider.Registry{"dnsgate": {
		Fields: fields,
		New: func(m map[string]string, opts ...dnsprovider.Option) (dnsprovider.Provider, error) {
			fb.mu.Lock()
			fb.built = append(fb.built, maps.Clone(m))
			fb.mu.Unlock()

			return inner.New(map[string]string{"token": m["token"]}, opts...)
		},
	}}
}

func newOptionsHarness(t *testing.T, s *store.Store, f *fakeProvider, fb *fieldBuilds) *harness {
	t.Helper()

	return newHarnessWith(t, s, tui.Deps{
		Providers:    optionsRegistry(f, fb, optionFields()),
		UnsealMethod: fastPassphrase(),
	})
}

func TestAFieldWithADefaultMayBeLeftEmpty(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	fb := &fieldBuilds{}
	h := newOptionsHarness(t, s, newFake(), fb)

	h.press("shift+tab", "i", "tab")
	h.typeText("edge")
	h.press("tab")
	h.typeText("gw-token")

	// The region's hint names the default it takes when left empty.
	h.press("tab")
	h.assertShows("eu-west")

	h.press("tab", "tab", "enter") // past the egress to the Save button, then save
	h.unsealWith(passphrase)

	h.assertShows("Discovered no Zones for dnsgate.edge")
	h.assertHides("Enter the Region")

	if got := storedFields(t, s, accountLabeled(t, s, "dnsgate.edge")); got["token"] != "gw-token" || got["region"] != "" {
		t.Errorf("sealed fields = %v, want the token and no region", got)
	}
}

func TestAFieldWithChoicesIsPickedFromThem(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	fb := &fieldBuilds{}
	h := newOptionsHarness(t, s, newFake(), fb)

	h.press("shift+tab", "i", "tab")
	h.typeText("edge")
	h.press("tab")
	h.typeText("gw-token")
	h.press("tab", "tab")

	// The egress starts at its default, and is not typed into.
	h.assertShows("‹ auto ›")
	h.typeText("x")
	h.assertShows("‹ auto ›")

	// Right and Left step through the choices.
	h.press("right")
	h.assertShows("‹ gateway ›")
	h.press("left")
	h.assertShows("‹ auto ›")

	// Enter lists them to pick one.
	h.press("enter")
	h.assertShows("auto", "gateway", "agent")
	h.press("down", "down", "enter")
	h.assertShows("‹ agent ›")

	h.press("tab", "enter") // to the Save button, then save
	h.unsealWith(passphrase)

	h.assertShows("Discovered no Zones for dnsgate.edge")

	if got := storedFields(t, s, accountLabeled(t, s, "dnsgate.edge")); got["egress"] != "agent" {
		t.Errorf("sealed fields = %v, want egress agent", got)
	}
}

func TestEditingAnAccountLeavesAFieldWithADefaultEmpty(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newOptionsHarness(t, s, newFake(), &fieldBuilds{})

	h.press("shift+tab", "i", "tab")
	h.typeText("edge")
	h.press("tab")
	h.typeText("gw-token")
	h.press("tab", "tab", "tab", "enter")
	h.unsealWith(passphrase)
	h.assertShows("Discovered no Zones for dnsgate.edge")

	// Its stored credentials hold no region: a new token still saves.
	h.press("down", "e")
	h.assertShows("Edit Account dnsgate.edge")
	h.press("down")
	h.typeText("gw-token-2")
	h.press("tab", "tab", "tab", "enter") // past the region and egress to Save

	h.assertShows("Saved dnsgate.edge.")

	if got := storedFields(t, s, accountLabeled(t, s, "dnsgate.edge")); got["token"] != "gw-token-2" || got["region"] != "" {
		t.Errorf("sealed fields = %v, want the new token and no region", got)
	}
}

func TestUnreadableCredentialsStartAPickedFieldAtItsDefault(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	// With two secrets, an old single API token cannot be read into them.
	fields := append(optionFields(), dnsprovider.Field{Name: "key", Label: "API key", Secret: true})
	addOldAccount(t, s, "dnsgate", "old", "only-a-token")
	h := newHarnessWith(t, s, tui.Deps{
		Providers:    optionsRegistry(newFake(), &fieldBuilds{}, fields),
		UnsealMethod: fastPassphrase(),
	})

	h.press("shift+tab", "j", "e")
	h.unsealWith(passphrase)
	h.assertShows("Edit Account dnsgate.old", "cannot read the stored credentials")

	h.typeText("2")
	h.press("down", "down", "down") // past the token and region to the egress
	h.assertShows("‹ auto ›")

	// The default changes nothing: a rename reseals the credentials as
	// they are.
	h.press("down", "down", "enter") // past the key to the Save button
	h.assertShows("Saved dnsgate.old2.")

	if got := storedPlaintext(t, s, accountLabeled(t, s, "dnsgate.old2")); got != "only-a-token" {
		t.Errorf("renaming resealed the credentials as %q, want them as they were", got)
	}
}
