package tui

import (
	"context"
	"errors"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sergelogvinov/dnscli/internal/store"
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
	// method Unseals: the passphrase, or a method that asks the user
	// itself, such as the Keychain with Touch ID.
	method     vault.Method
	enroll     bool
	note       string // why the prompt asks for the passphrase
	field      int
	passphrase string
	repeat     string
	caret      int // the focused field's caret, in runes back from its end
	err        string
	busy       bool // the KDF is running
}

// unsealedMsg reports the result of Unseal or of enrolling a passphrase.
type unsealedMsg struct {
	key   *vault.Key
	slots []vault.Slot // the key slots in the Store; nil when unread
	err   error
}

// sealed reports whether the session is sealed, and so read-only.
func (m *Model) sealed() bool {
	return m.key == nil
}

// withSecrets runs next, which needs Account secrets, once the session is
// unsealed. A sealed session first prompts for Unseal: Unseal is deferred
// until the first action that needs a secret. An Unseal prompt that is
// already open is kept: next runs after the same Unseal, with the action
// already waiting for it.
func (m *Model) withSecrets(next func() tea.Cmd) tea.Cmd {
	if !m.sealed() {
		return next()
	}

	if m.unseal != nil {
		waiting := m.afterUnseal
		m.afterUnseal = next

		if waiting != nil {
			m.afterUnseal = func() tea.Cmd { return tea.Batch(waiting(), next()) }
		}

		return nil
	}

	m.afterUnseal = next

	// A method that asks the user itself, such as Touch ID, starts at once.
	method := m.deps.unsealMethod()
	if !vault.AsksForSecret(method) && hasKeySlot(m.slots, method) {
		m.unseal = &unsealPrompt{method: method}

		return m.startUnseal(nil)
	}

	// Any key slot means a data key exists: a new one would not open the
	// secrets sealed with it. The Store has the final say when submitting.
	// The first data key is always protected by a passphrase, for recovery.
	m.unseal = &unsealPrompt{method: m.deps.passphrase(), enroll: len(m.slots) == 0}
	if !vault.AsksForSecret(method) && len(m.slots) > 0 {
		m.unseal.note = "The " + methodTitle(method) + " is not enrolled: enter your passphrase. " +
			m.keys.keysFor(ActionEnrollKeychain) + " enrolls it once the session is unsealed."
	}

	return nil
}

func hasKeySlot(slots []vault.Slot, m vault.Method) bool {
	return slices.ContainsFunc(slots, func(s vault.Slot) bool { return s.Method == m.Name() })
}

// methodTitle names an Unseal Method for the user.
func methodTitle(m vault.Method) string {
	if m.Name() == (vault.Keychain{}).Name() {
		return "Keychain"
	}

	return m.Name()
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

	if m.keys.typeInto(text, &p.caret, msg) {
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

	return m.startUnseal([]byte(p.passphrase))
}

// startUnseal Unseals, or enrolls the first data key, with the prompt's
// method in the background.
func (m *Model) startUnseal(secret []byte) tea.Cmd {
	p := m.unseal
	p.busy = true
	method, store, ctx, enroll := p.method, m.deps.Store, m.ctx, p.enroll

	return func() tea.Msg {
		return unsealOrEnroll(ctx, store, method, enroll, secret)
	}
}

// unsealOrEnroll decides from the Store, not from the cached key slots,
// whether to Unseal or to enroll a new data key. Once any key slot exists,
// even one another process enrolled a moment ago, it only Unseals.
func unsealOrEnroll(ctx context.Context, s *store.Store, method vault.Method, enroll bool, secret []byte) unsealedMsg {
	slots, err := s.KeySlots(ctx)
	if err != nil {
		return unsealedMsg{err: err}
	}

	if len(slots) == 0 {
		if !enroll {
			return unsealedMsg{slots: slots, err: vault.ErrNoSlot}
		}

		key, slot, err := vault.Create(method, secret)
		if err != nil {
			return unsealedMsg{err: err}
		}

		slot, err = s.EnrollFirstKeySlot(ctx, slot)
		if err == nil {
			return unsealedMsg{key: key, slots: []vault.Slot{slot}}
		}

		if !errors.Is(err, store.ErrAlreadyEnrolled) {
			return unsealedMsg{err: err}
		}

		// Another process enrolled first: Unseal with its data key.
		if slots, err = s.KeySlots(ctx); err != nil {
			return unsealedMsg{err: err}
		}
	}

	key, err := vault.Unseal(method, slots, secret)

	return unsealedMsg{key: key, slots: slots, err: err}
}

// unsealed handles the result of Unseal: on success it runs the action
// that was waiting for it.
func (m *Model) unsealed(msg unsealedMsg) tea.Cmd {
	if m.unseal == nil {
		return nil
	}

	if msg.slots != nil {
		m.slots = msg.slots
	}

	if msg.err != nil && !vault.AsksForSecret(m.unseal.method) {
		// When Touch ID fails, the passphrase still Unseals.
		m.unseal = &unsealPrompt{
			method: m.deps.passphrase(),
			err:    "Could not Unseal: " + msg.err.Error() + ". Enter your passphrase instead.",
		}

		return nil
	}

	if msg.err != nil {
		p := m.unseal
		// A passphrase enrolled elsewhere turns enrolling into Unseal.
		enrolledElsewhere := p.enroll && len(m.slots) > 0

		p.busy = false
		p.passphrase, p.repeat = "", ""
		p.field = unsealFieldPassphrase
		if enrolledElsewhere {
			p.enroll = false
		}

		switch {
		case enrolledElsewhere && errors.Is(msg.err, vault.ErrWrongPassphrase):
			p.err = "A passphrase is already enrolled: enter it to Unseal. The session is still sealed."
		case errors.Is(msg.err, vault.ErrWrongPassphrase):
			p.err = "Wrong passphrase. The session is still sealed."
		default:
			p.err = "Could not Unseal: " + msg.err.Error() + ". The session is still sealed."
		}

		return nil
	}

	m.key = msg.key

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

	if !vault.AsksForSecret(p.method) {
		return dialogView(title, nil, 0, false, []string{
			"This action needs your Account secrets.",
			"",
			hintStyle.Render("Confirm with Touch ID to Unseal…"),
		})
	}

	var notes []string

	if p.note != "" {
		notes = append(notes, wrapNote(lipgloss.NewStyle(), p.note, noteWidth)...)
		notes = append(notes, "")
	}

	if p.enroll {
		title = "Choose a passphrase"
		fields = append(fields, dialogField{label: "Repeat", value: p.repeat, masked: true})
		notes = append(notes, wrapNote(lipgloss.NewStyle(), "The passphrase protects the credentials of every Account. dnscli asks for it once per session.", noteWidth)...)
		notes = append(notes, "")
		notes = append(notes, wrapNote(hintStyle, notDisposableWarning, noteWidth)...)
	} else {
		notes = append(notes, "This action needs your Account secrets.")
	}

	if p.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, p.err, noteWidth)...)
	}

	if p.busy {
		notes = append(notes, "", hintStyle.Render("unsealing…"))
	}

	notes = append(notes, "", hintStyle.Render(m.keys.keysFor(ActionSelect)+" Unseal · "+m.keys.keysFor(ActionClose)+" cancel"))

	if p.field < len(fields) {
		fields[p.field].caret = p.caret
	}

	return dialogView(title, fields, p.field, !p.busy, notes)
}
