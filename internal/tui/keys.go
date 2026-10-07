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
	ActionSelect
	ActionSearch
	ActionHelp
	ActionClose
	ActionQuit
	// ActionAllAccounts clears the Account filter, so the Domains pane shows
	// the Zones of every Account.
	ActionAllAccounts
	// ActionAddAccount opens the add-Account dialog.
	ActionAddAccount
	// ActionEnrollKeychain enrolls the Keychain with Touch ID as another
	// Unseal Method, after Unseal.
	ActionEnrollKeychain
	// ActionRemoveAccount asks to remove the highlighted Account with its
	// cached Zones, Records and secret.
	ActionRemoveAccount
	// ActionRelistZones re-lists the highlighted Account's Zones from its
	// Provider, after Unseal.
	ActionRelistZones
	// ActionRefresh Refreshes the open Zone again, such as to retry a
	// failed Refresh.
	ActionRefresh
	// ActionDelegation shows the highlighted Zone's Delegation next to the
	// nameservers its Account expects, read-only (ADR-0004).
	ActionDelegation
	// ActionAddRecord opens the Record form to add a Record to the open
	// Zone's pending Changeset. Like editing and deleting, it obeys the
	// write gate.
	ActionAddRecord
	// ActionEditRecord opens the Record form, prefilled, for the Record
	// highlighted in the Records pane.
	ActionEditRecord
	// ActionDeleteRecord marks the highlighted Record for deletion, or
	// takes back its pending change.
	ActionDeleteRecord
	// ActionPreviewChanges shows the open Zone's pending Changeset, from
	// which it is applied or discarded.
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
)

// Binding maps one Action to its keys.
type Binding struct {
	Action Action
	// Keys are key names as Bubble Tea prints them, e.g. "tab", "shift+tab", "j".
	Keys []string
	// Help is the description shown in the help overlay.
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
			{Action: ActionSelect, Keys: []string{"enter"}, Help: "open / select"},
			{Action: ActionSearch, Keys: []string{"/"}, Help: "search"},
			{Action: ActionAllAccounts, Keys: []string{"*"}, Help: "Zones of all Accounts"},
			{Action: ActionRefresh, Keys: []string{"r"}, Help: "Refresh the open Zone (retry)"},
			{Action: ActionDelegation, Keys: []string{"N"}, Help: "Delegation (nameservers), read-only"},
			{Action: ActionAddRecord, Keys: []string{"i", "insert"}, Help: "insert a new Record (Records pane)"},
			{Action: ActionEditRecord, Keys: []string{"e"}, Help: "edit the Record (Records pane; Enter too)"},
			{Action: ActionDeleteRecord, Keys: []string{"d", "delete"}, Help: "delete the Record, or undo its change (Records pane)"},
			{Action: ActionApplyTemplate, Keys: []string{"t"}, Help: "apply a Template, such as Google mail (Records pane)"},
			{Action: ActionPreviewChanges, Keys: []string{"p"}, Help: "preview the pending changes, Enter applies"},
			{Action: ActionDiscardChanges, Keys: []string{"D"}, Help: "discard the pending changes (in the preview)"},
			{Action: ActionApplyOver, Keys: []string{"O"}, Help: "apply over the current values (in the preview, after a conflict)"},
			{Action: ActionAddAccount, Keys: []string{"a"}, Help: "add Account"},
			{Action: ActionRemoveAccount, Keys: []string{"X"}, Help: "remove Account"},
			{Action: ActionRelistZones, Keys: []string{"L"}, Help: "re-list the Account's Zones"},
			{Action: ActionEnrollKeychain, Keys: []string{"K"}, Help: "enroll the Keychain (Touch ID)"},
			{Action: ActionHelp, Keys: []string{"?"}, Help: "toggle help"},
			{Action: ActionClose, Keys: []string{"esc"}, Help: "close"},
			{Action: ActionQuit, Keys: []string{"q", "ctrl+c"}, Help: "quit"},
		},
	}
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
	case "esc":
		return "Esc"
	case "ctrl+c":
		return "Ctrl+C"
	}

	return k
}
