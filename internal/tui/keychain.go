package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sergelogvinov/dnscli/internal/vault"
)

// keychainConfirm asks before enrolling the Keychain as an Unseal Method.
type keychainConfirm struct {
	err  string
	busy bool // the key slot is being stored
}

// keychainEnrolledMsg reports the result of enrolling the Keychain.
type keychainEnrolledMsg struct {
	slots []vault.Slot // every key slot after enrolling
	err   error
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
		c.err = ""
		key, method, store, ctx := m.key, m.deps.Keychain, m.deps.Store, m.ctx

		return func() tea.Msg {
			slot, err := key.Wrap(method, nil)
			if err != nil {
				return keychainEnrolledMsg{err: err}
			}

			if _, _, err := store.EnrollKeySlot(ctx, slot); err != nil {
				return keychainEnrolledMsg{err: err}
			}

			slots, err := store.KeySlots(ctx)

			return keychainEnrolledMsg{slots: slots, err: err}
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

		return
	}

	m.keychain = nil
	m.slots = msg.slots
	m.notice = notice{text: "Keychain enrolled: Touch ID now Unseals dnscli. Your passphrase stays enrolled for recovery."}
}

func (m *Model) keychainView() string {
	c := m.keychain

	notes := wrapNote(lipgloss.NewStyle(), "Unseal with Touch ID from now on: the Keychain keeps a key that wraps the same data key.", noteWidth)
	notes = append(notes, wrapNote(lipgloss.NewStyle(), "Your passphrase stays enrolled for recovery.", noteWidth)...)

	if c.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, c.err, noteWidth)...)
	}

	if c.busy {
		notes = append(notes, "", hintStyle.Render("enrolling…"))
	}

	notes = append(notes, "", hintStyle.Render(m.keys.keysFor(ActionSelect)+" enroll · "+m.keys.keysFor(ActionClose)+" cancel"))

	return dialogView("Enroll the Keychain", nil, 0, false, notes)
}
