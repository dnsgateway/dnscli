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
	ActionUp
	ActionDown
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
			{Action: ActionUp, Keys: []string{"up", "k"}, Help: "move up"},
			{Action: ActionDown, Keys: []string{"down", "j"}, Help: "move down"},
			{Action: ActionSelect, Keys: []string{"enter"}, Help: "open / select"},
			{Action: ActionSearch, Keys: []string{"/"}, Help: "search"},
			{Action: ActionAllAccounts, Keys: []string{"*"}, Help: "Zones of all Accounts"},
			{Action: ActionAddAccount, Keys: []string{"a"}, Help: "add Account"},
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
