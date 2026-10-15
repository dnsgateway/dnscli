package tui

import (
	"errors"
	"fmt"
	"strings"
)

// Action is something the user can do. Action handling is keyed by Action,
// never by key; keys reach actions only through a Shortcut Profile.
type Action int

// Actions in v1.
const (
	ActionNone Action = iota
	ActionNextPane
	ActionPrevPane
	// ActionPaneRight moves focus from the Domains or Accounts pane to the
	// Records pane, on their right.
	ActionPaneRight
	// ActionPaneLeft moves focus from the Records pane to the Domains pane,
	// on its left.
	ActionPaneLeft
	ActionUp
	ActionDown
	// ActionPageUp and ActionPageDown move the focused pane's cursor by
	// the rows the pane shows, scrolling its list a page.
	ActionPageUp
	ActionPageDown
	// ActionSelect selects the highlighted item: in the Domains and
	// Accounts panes as ActionShow does, in the Records pane as ActionEdit
	// does. In dialogs it moves on or confirms.
	ActionSelect
	ActionSearch
	ActionHelp
	ActionClose
	ActionQuit
	// ActionShow shows what the highlighted item holds: in the Domains
	// pane the Zone's Records, and in the Accounts pane only the Account's
	// Zones, or every Account's again when that Account is already the
	// filter.
	ActionShow
	// ActionEnrollKeychain enrolls the Keychain with Touch ID as another
	// Unseal Method, after Unseal.
	ActionEnrollKeychain
	// ActionRelistZones re-lists the highlighted Account's Zones from its
	// Provider, after Unseal.
	ActionRelistZones
	// ActionRefresh acts on the focused pane. In the Records pane it
	// Refreshes the open Zone again, such as to retry a failed
	// Refresh; in the Domains and Accounts panes it re-lists the Zones of
	// the Account shown, or of every Account when no Account is.
	ActionRefresh
	// ActionInsert, ActionEdit and ActionDelete act on the focused pane's
	// items. In the Records pane, insert opens the Record form to add a
	// Record to the open Zone's pending Changeset, edit opens it prefilled
	// for the highlighted Record, and delete marks that Record for
	// deletion or takes back its pending change; all three obey the write
	// gate. In the Accounts pane, insert opens the add-Account dialog, edit
	// the edit-Account dialog for the highlighted Account, and delete asks
	// to remove it with its cached Zones, Records and secret. In the
	// Domains pane, insert Onboards a zone name in an Account, and its
	// Tenant, chosen in the dialog, edit opens the Edit Zone
	// dialog, which shows the highlighted Zone's settings and Delegation,
	// read-only, in any session, and changes its DNSSEC, HSTS or minimum
	// TLS version when the Provider can, and delete Offboards the
	// highlighted Zone, never the Live Zone; Onboarding, Offboarding and
	// the settings are Zone writes, each once confirmed, after Unseal.
	// Without Accounts, insert adds one from any pane.
	ActionInsert
	ActionEdit
	ActionDelete
	// ActionPreviewChanges, in the Records pane, shows the open Zone's
	// pending Changeset, from which it is applied or discarded.
	ActionPreviewChanges
	// ActionDiscardChanges discards the pending Changeset shown in the preview.
	ActionDiscardChanges
	// ActionApplyOver, in the preview after an Apply stopped on a
	// conflict, rebases the conflicting changes onto the Records as the
	// Provider has them now, and applies the Changeset.
	ActionApplyOver
	// ActionApplyTemplate opens the Template dialog, which stages a
	// built-in Template's Records into the open Zone's pending Changeset.
	// Like editing, it obeys the write gate.
	ActionApplyTemplate
	// ActionDNSSECStep takes the open Zone's next DNSSEC step, the one
	// the Zone Status bar offers, such as turning signing on, a Zone
	// write, or publishing, replacing or removing the DS at the registrar,
	// a Delegation write: each is confirmed first.
	ActionDNSSECStep
)

// Binding maps one Action to its keys.
type Binding struct {
	Action Action
	// Keys are key names as Bubble Tea prints them, e.g. "tab", "shift+tab", "j".
	Keys []string
	// Help is the description shown in the help overlay. An Action that
	// acts on a pane's items has none: the overlay shows paneHelp's, which
	// says what it does in the focused pane.
	Help string
}

// ShortcutProfile is a named mapping from user actions to keys.
type ShortcutProfile struct {
	Name     string
	Bindings []Binding
}

// DefaultShortcutProfile is the Shortcut Profile v1 ships with.
func DefaultShortcutProfile() ShortcutProfile {
	return ShortcutProfile{
		Name: "default",
		Bindings: []Binding{
			{Action: ActionNextPane, Keys: []string{"tab"}, Help: "next pane"},
			{Action: ActionPrevPane, Keys: []string{"shift+tab"}, Help: "previous pane"},
			{Action: ActionPaneRight, Keys: []string{"right"}, Help: "Records pane"},
			{Action: ActionPaneLeft, Keys: []string{"left"}, Help: "Domains pane"},
			{Action: ActionUp, Keys: []string{"up", "k"}, Help: "move up"},
			{Action: ActionDown, Keys: []string{"down", "j"}, Help: "move down"},
			{Action: ActionPageUp, Keys: []string{"pgup"}, Help: "page up"},
			{Action: ActionPageDown, Keys: []string{"pgdown"}, Help: "page down"},
			{Action: ActionSelect, Keys: []string{"enter"}},
			{Action: ActionSearch, Keys: []string{"/"}, Help: "search"},
			{Action: ActionShow, Keys: []string{" "}},
			{Action: ActionRefresh, Keys: []string{"r"}},
			{Action: ActionInsert, Keys: []string{"i", "insert"}},
			{Action: ActionEdit, Keys: []string{"e"}},
			{Action: ActionDelete, Keys: []string{"d", "delete"}},
			{Action: ActionApplyTemplate, Keys: []string{"t"}},
			{Action: ActionPreviewChanges, Keys: []string{"P"}},
			{Action: ActionDiscardChanges, Keys: []string{"D"}},
			{Action: ActionApplyOver, Keys: []string{"O"}},
			{Action: ActionRelistZones, Keys: []string{"L"}},
			{Action: ActionDNSSECStep, Keys: []string{"S"}, Help: "take the open Zone's next DNSSEC step, such as turning it on, or publishing, replacing or removing its DS"},
			{Action: ActionEnrollKeychain, Keys: []string{"K"}, Help: "enroll the Keychain (Touch ID)"},
			{Action: ActionHelp, Keys: []string{"?"}, Help: "toggle help"},
			{Action: ActionClose, Keys: []string{"esc"}, Help: "close"},
			{Action: ActionQuit, Keys: []string{"q", "ctrl+c"}, Help: "quit"},
		},
	}
}

// paneBinding is an Action as the help overlay describes it in one pane.
type paneBinding struct {
	Action Action
	Help   string
}

// paneHelp lists the Actions that act on each pane's items, with what they
// do there. The help overlay shows the focused pane's apart from the
// global ones: every other bound Action.
var paneHelp = map[pane][]paneBinding{
	paneDomains: {
		{ActionShow, "show the Zone's Records"},
		{ActionInsert, "Onboard a zone name in an Account (starts hosting, never registers)"},
		{ActionEdit, "edit the Zone: its DNSSEC, HSTS or minimum TLS version, and its Delegation and DNSSEC keys, read-only"},
		{ActionDelete, "Offboard the Zone, not the Live Zone (stops hosting, never touches the registration)"},
		{ActionRefresh, "re-list the Zones of the Account shown, or of every Account"},
	},
	paneRecords: {
		{ActionInsert, "insert a new Record"},
		{ActionEdit, "edit the Record"},
		{ActionDelete, "delete the Record, or undo its change"},
		{ActionRefresh, "Refresh the open Zone (retry)"},
		{ActionApplyTemplate, "apply a Template, such as Google mail"},
		{ActionPreviewChanges, "preview the pending changes, Enter applies"},
		{ActionDiscardChanges, "discard the pending changes (in the preview)"},
		{ActionApplyOver, "apply over the current values (in the preview, after a conflict)"},
	},
	paneAccounts: {
		{ActionShow, "show only the Account's Zones, or every Account's again"},
		{ActionInsert, "add an Account"},
		{ActionEdit, "edit the Account: its name or credentials"},
		{ActionDelete, "remove the Account"},
		{ActionRelistZones, "re-list the Account's Zones"},
		{ActionRefresh, "re-list the Zones of the Account shown, or of every Account"},
	},
}

// sameAs maps, per pane, an Action to another that does the same there, so
// the help overlay lists both Actions' keys on the first one's line: Select
// shows in the Domains and Accounts panes, and edits in the Records pane.
var sameAs = map[pane]map[Action]Action{
	paneDomains:  {ActionShow: ActionSelect},
	paneAccounts: {ActionShow: ActionSelect},
	paneRecords:  {ActionEdit: ActionSelect},
}

// paneAction reports whether Action a acts on some pane's items.
func paneAction(a Action) bool {
	for _, same := range sameAs {
		for _, s := range same {
			if s == a {
				return true
			}
		}
	}

	for _, bs := range paneHelp {
		for _, b := range bs {
			if b.Action == a {
				return true
			}
		}
	}

	return false
}

// Validate reports whether the profile is usable: every key maps to one
// Action, and at least one key quits.
func (p ShortcutProfile) Validate() error {
	seen := map[string]bool{}
	canQuit := false

	for _, b := range p.Bindings {
		for _, k := range b.Keys {
			if seen[k] {
				return fmt.Errorf("key %q is bound more than once", k)
			}

			seen[k] = true
			canQuit = canQuit || b.Action == ActionQuit
		}
	}

	if !canQuit {
		return errors.New("no quit key")
	}

	return nil
}

// keymap resolves keys to actions for a Shortcut Profile.
type keymap struct {
	profile ShortcutProfile
	actions map[string]Action
}

func newKeymap(p ShortcutProfile) keymap {
	km := keymap{profile: p, actions: map[string]Action{}}

	for _, b := range p.Bindings {
		for _, k := range b.Keys {
			km.actions[k] = b.Action
		}
	}

	return km
}

// action returns the Action bound to key, or ActionNone.
func (km keymap) action(key string) Action {
	return km.actions[key]
}

// keysFor returns the display form of the keys bound to an Action, e.g. "↑/k".
func (km keymap) keysFor(a Action) string {
	for _, b := range km.profile.Bindings {
		if b.Action == a {
			names := make([]string, len(b.Keys))
			for i, k := range b.Keys {
				names[i] = displayKey(k)
			}

			return strings.Join(names, "/")
		}
	}

	return ""
}

// recordsKeys returns the display form of the keys bound to an Action that
// acts in the Records pane, saying so when another pane has focus, e.g.
// "r in the Records pane".
func (m *Model) recordsKeys(a Action) string {
	if m.focus == paneRecords {
		return m.keys.keysFor(a)
	}

	return m.keys.keysFor(a) + " in the Records pane"
}

func displayKey(k string) string {
	switch k {
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "shift+tab":
		return "Shift+Tab"
	case "tab":
		return "Tab"
	case "enter":
		return "Enter"
	case " ":
		return "Space"
	case "esc":
		return "Esc"
	case "ctrl+c":
		return "Ctrl+C"
	}

	return k
}
