// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui

import (
	"errors"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sergelogvinov/dnscli/internal/vault"
)

// keychainConfirm asks before enrolling the Keychain as an Unseal Method.
type keychainConfirm struct {
	err string
	// advice follows err, such as to keep using the passphrase.
	advice string
	busy   bool // the key slot is being stored
}

// keychainEnrolledMsg reports the result of enrolling the Keychain.
type keychainEnrolledMsg struct {
	slot vault.Slot // the enrolled key slot
	err  error
	// cleanupErr is why a replaced key slot's Keychain item could not be
	// deleted; the enrollment itself succeeded.
	cleanupErr error
}

// enrollKeychain asks to enroll the Keychain. It wraps the session's data
// key, so a sealed session Unseals first.
func (m *Model) enrollKeychain() tea.Cmd {
	if m.deps.Keychain == nil {
		m.notice = notice{text: "The Keychain Unseal Method is not available on this platform.", err: true}

		return nil
	}

	return m.withSecrets(func() tea.Cmd {
		m.keychain = &keychainConfirm{}

		return nil
	})
}

// keychainKey handles a key while the Keychain confirmation is open.
func (m *Model) keychainKey(msg tea.KeyMsg) tea.Cmd {
	c := m.keychain
	a := m.keys.action(msg.String())

	if a == ActionQuit {
		return m.handle(ActionQuit)
	}

	if c.busy {
		return nil
	}

	switch a {
	case ActionClose:
		m.keychain = nil
	case ActionSelect:
		c.busy = true
		c.err, c.advice = "", ""
		key, method, store, ctx := m.key, m.deps.Keychain, m.deps.Store, m.ctx

		return func() tea.Msg {
			slot, err := key.Wrap(method, nil)
			if err != nil {
				return keychainEnrolledMsg{err: err}
			}

			enrolled, replaced, err := store.EnrollKeySlot(ctx, slot)
			if err != nil {
				// Nothing references the new Keychain item.
				_ = vault.Discard(method, slot)

				return keychainEnrolledMsg{err: err}
			}

			// The key slot is committed: whatever follows, enrolling succeeded.
			msg := keychainEnrolledMsg{slot: enrolled}
			for _, old := range replaced {
				if err := vault.Discard(method, old); err != nil {
					msg.cleanupErr = err
				}
			}

			return msg
		}
	}

	return nil
}

func (m *Model) keychainEnrolled(msg keychainEnrolledMsg) {
	if m.keychain == nil {
		return
	}

	if msg.err != nil {
		m.keychain.busy = false
		m.keychain.err = "Could not enroll the Keychain: " + msg.err.Error()
		m.keychain.advice = ""

		if errors.Is(msg.err, vault.ErrTouchIDUnavailable) {
			// Nothing was stored: enrolling cannot succeed on this Mac or build.
			m.keychain.err = msg.err.Error()
			m.keychain.advice = "The Keychain is not enrolled: keep using the passphrase."
		}

		return
	}

	m.keychain = nil
	// The new key slot replaced the method's earlier ones.
	m.slots = append(slices.DeleteFunc(slices.Clone(m.slots), func(s vault.Slot) bool {
		return s.Method == msg.slot.Method
	}), msg.slot)
	m.notice = notice{text: "Keychain enrolled: Touch ID now Unseals dnscli. Your passphrase stays enrolled for recovery."}

	if msg.cleanupErr != nil {
		m.notice.text += " The replaced Keychain item could not be deleted: " + msg.cleanupErr.Error()
	}
}

func (m *Model) keychainView() string {
	c := m.keychain

	notes := wrapNote(lipgloss.NewStyle(), "Unseal with Touch ID from now on: the Keychain keeps a key that wraps the same data key.", noteWidth)
	notes = append(notes, wrapNote(lipgloss.NewStyle(), "Your passphrase stays enrolled for recovery.", noteWidth)...)

	if c.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, c.err, noteWidth)...)
	}

	if c.advice != "" {
		notes = append(notes, wrapNote(lipgloss.NewStyle(), c.advice, noteWidth)...)
	}

	if c.busy {
		notes = append(notes, "", hintStyle.Render("enrolling…"))
	}

	notes = append(notes, "", hintStyle.Render(m.keys.keysFor(ActionSelect)+" enroll · "+m.keys.keysFor(ActionClose)+" cancel"))

	return dialogView("Enroll the Keychain", nil, 0, false, notes)
}
