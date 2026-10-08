package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/vault"
)

// assertDialogFits checks that the dialog titled title is drawn whole: its
// top border reaches its right corner inside the terminal.
func (h *harness) assertDialogFits(title string) {
	h.t.Helper()

	for _, line := range strings.Split(h.view(), "\n") {
		if _, rest, ok := strings.Cut(line, "━ "+title+" "); ok {
			if !strings.Contains(rest, "┓") {
				h.t.Fatalf("dialog %q is cut off by the terminal edge:\n%s", title, h.view())
			}

			return
		}
	}

	h.t.Fatalf("dialog %q is not shown:\n%s", title, h.view())
}

func TestALongMaskedTokenKeepsTheDialogInsideTheTerminal(t *testing.T) {
	s, _ := openStore(t)
	h := newAccountHarness(t, s, newFake())
	h.send(tea.WindowSizeMsg{Width: 80, Height: 30})

	h.press("i", "tab")
	h.typeText("work")
	h.press("tab")
	h.typeText(strings.Repeat("t", 200))

	h.assertDialogFits("Add Account")
	h.assertShows("•••")
}

func TestALongUnsealErrorIsWrappedInsideTheDialog(t *testing.T) {
	s, _ := openStore(t)

	// Only another Unseal Method is enrolled, so the passphrase opens no key slot.
	if _, err := s.EnrollFirstKeySlot(context.Background(), vault.Slot{Method: "keychain", Params: []byte("{}"), Wrapped: []byte{1}}); err != nil {
		t.Fatal(err)
	}

	h := newAccountHarness(t, s, newFake())
	h.send(tea.WindowSizeMsg{Width: 80, Height: 30})

	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("enter")

	h.assertShows("Could not Unseal", "still sealed")
	h.assertDialogFits("Unseal")
}
