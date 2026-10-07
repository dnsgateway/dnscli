package tui

import (
	"errors"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sergelogvinov/dnscli/internal/vault"
)

// Fields of the Unseal prompt. Enrolling a passphrase asks for it twice.
const (
	unsealFieldPassphrase = iota
	unsealFieldRepeat
)

// unsealPrompt asks for the passphrase that Unseals the session, or, when
// none is enrolled yet, for a new one.
type unsealPrompt struct {
	enroll     bool
	field      int
	passphrase string
	repeat     string
	err        string
	busy       bool // the KDF is running
}

// unsealedMsg reports the result of Unseal or of enrolling a passphrase.
type unsealedMsg struct {
	key  *vault.Key
	slot *vault.Slot // the newly enrolled key slot
	err  error
}

// sealed reports whether the session is sealed, and so read-only.
func (m *Model) sealed() bool {
	return m.key == nil
}

// withSecrets runs next, which needs Account secrets, once the session is
// unsealed. A sealed session first prompts for Unseal: Unseal is deferred
// until the first action that needs a secret.
func (m *Model) withSecrets(next func() tea.Cmd) tea.Cmd {
	if !m.sealed() {
		return next()
	}

	m.afterUnseal = next
	m.unseal = &unsealPrompt{enroll: !slices.ContainsFunc(m.slots, func(s vault.Slot) bool {
		return s.Method == m.deps.unsealMethod().Name()
	})}

	return nil
}

// unsealKey handles a key while the Unseal prompt is open.
func (m *Model) unsealKey(msg tea.KeyMsg) tea.Cmd {
	p := m.unseal
	a := m.keys.action(msg.String())

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if p.busy {
		return nil
	}

	text := &p.passphrase
	if p.field == unsealFieldRepeat {
		text = &p.repeat
	}

	if m.keys.typeInto(text, msg) {
		return nil
	}

	last := unsealFieldPassphrase
	if p.enroll {
		last = unsealFieldRepeat
	}

	switch a {
	case ActionClose:
		m.unseal = nil
		m.afterUnseal = nil
	case ActionNextPane, ActionPrevPane:
		if p.enroll {
			p.field = 1 - p.field
		}
	case ActionSelect:
		if p.field < last {
			p.field++

			return nil
		}

		return m.submitUnseal()
	}

	return nil
}

func (m *Model) submitUnseal() tea.Cmd {
	p := m.unseal
	p.err = ""

	switch {
	case p.passphrase == "":
		p.err = "Enter a passphrase."

		return nil
	case p.enroll && p.passphrase != p.repeat:
		p.err = "The passphrases do not match."
		p.repeat = ""

		return nil
	}

	p.busy = true
	method, slots, store, ctx := m.deps.unsealMethod(), m.slots, m.deps.Store, m.ctx
	secret := []byte(p.passphrase)

	if !p.enroll {
		return func() tea.Msg {
			key, err := vault.Unseal(method, slots, secret)

			return unsealedMsg{key: key, err: err}
		}
	}

	return func() tea.Msg {
		key, slot, err := vault.Create(method, secret)
		if err != nil {
			return unsealedMsg{err: err}
		}

		slot, err = store.AddKeySlot(ctx, slot)
		if err != nil {
			return unsealedMsg{err: err}
		}

		return unsealedMsg{key: key, slot: &slot}
	}
}

// unsealed handles the result of Unseal: on success it runs the action
// that was waiting for it.
func (m *Model) unsealed(msg unsealedMsg) tea.Cmd {
	if m.unseal == nil {
		return nil
	}

	if msg.err != nil {
		m.unseal.busy = false
		m.unseal.passphrase, m.unseal.repeat = "", ""
		m.unseal.field = unsealFieldPassphrase

		m.unseal.err = "Could not Unseal: " + msg.err.Error() + ". The session is still sealed."
		if errors.Is(msg.err, vault.ErrWrongPassphrase) {
			m.unseal.err = "Wrong passphrase. The session is still sealed."
		}

		return nil
	}

	m.key = msg.key
	if msg.slot != nil {
		m.slots = append(m.slots, *msg.slot)
	}

	m.unseal = nil
	next := m.afterUnseal
	m.afterUnseal = nil

	if next == nil {
		return nil
	}

	return next()
}

func (m *Model) unsealView() string {
	p := m.unseal

	title, fields := "Unseal", []dialogField{{label: "Passphrase", value: p.passphrase, masked: true}}

	var notes []string

	if p.enroll {
		title = "Choose a passphrase"
		fields = append(fields, dialogField{label: "Repeat", value: p.repeat, masked: true})
		notes = append(notes, wrapNote(lipgloss.NewStyle(), "The passphrase protects the API tokens of every Account. dnscli asks for it once per session.", 56)...)
		notes = append(notes, "")
		notes = append(notes, wrapNote(hintStyle, notDisposableWarning, 56)...)
	} else {
		notes = append(notes, "This action needs your Account secrets.")
	}

	if p.err != "" {
		notes = append(notes, "", errorStyle.Render(p.err))
	}

	if p.busy {
		notes = append(notes, "", hintStyle.Render("unsealing…"))
	}

	notes = append(notes, "", hintStyle.Render(m.keys.keysFor(ActionSelect)+" Unseal · "+m.keys.keysFor(ActionClose)+" cancel"))

	return dialogView(title, fields, p.field, !p.busy, notes)
}
