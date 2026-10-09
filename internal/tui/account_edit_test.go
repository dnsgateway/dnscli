package tui_test

import (
	"context"
	"testing"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providertest"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/tui"
)

// relistToken re-lists the highlighted Account's Zones and returns the API
// token its Provider was built with.
func (h *harness) relistToken(f *fakeProvider) string {
	h.t.Helper()

	h.press("L")
	h.assertShows("Re-listed")

	for _, c := range f.Calls() {
		if c.Op == providertest.OpNew {
			return c.Fields["token"]
		}
	}

	h.t.Fatalf("the Provider was not built: %+v", f.Calls())

	return ""
}

func TestRenamingAnAccountKeepsItsZonesAndAPIToken(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := workZones()
	h := newAccountHarness(t, s, f)

	// Renaming reseals the API token for the new label: it Unseals first.
	h.press("shift+tab", "e")
	h.unsealWith(passphrase)

	h.assertShows("Edit Account cloudflare.work", "<provider>.<name>")
	h.press("down")
	h.assertShows("Type a new API token")
	h.press("up")

	h.press("backspace", "backspace", "backspace", "backspace")
	h.typeText("office")
	h.press("down", "down", "enter") // past the API token to Save

	h.assertHides("Edit Account")
	h.assertShows("Saved cloudflare.office.", "work.example", "work.test")

	accounts, err := s.Accounts(context.Background())
	if err != nil || len(accounts) != 1 || accounts[0].Label() != "cloudflare.office" {
		t.Fatalf("stored Accounts = %+v, %v", accounts, err)
	}

	if got := h.relistToken(f); got != token {
		t.Fatalf("the renamed Account's API token = %q, want %q", got, token)
	}
}

func TestEditingAnAccountReplacesItsAPIToken(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := workZones()
	h := newAccountHarness(t, s, f)

	h.press("shift+tab", "e")
	h.unsealWith(passphrase)

	h.press("tab")
	h.typeText("cf-token-rotated")
	h.press("enter", "enter")

	h.assertShows("Saved cloudflare.work.")

	if got := h.relistToken(f); got != "cf-token-rotated" {
		t.Fatalf("the Account's API token = %q, want the new one", got)
	}
}

func TestAnAccountCannotBeRenamedToAnotherAccountsName(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	withPersonalAccount(t, s)
	h := newAccountHarness(t, s, newFake())

	// Accounts are listed by label: cloudflare.personal, then cloudflare.work.
	h.press("shift+tab", "j", "e")
	h.unsealWith(passphrase)

	h.press("backspace", "backspace", "backspace", "backspace")
	h.typeText("personal")
	h.press("down", "down", "enter")

	h.assertShows("Edit Account", "cloudflare.personal already exists.")

	// Esc cancels: the Account keeps its name.
	h.press("esc")
	h.assertHides("Edit Account")

	accounts, err := s.Accounts(context.Background())
	if err != nil || len(accounts) != 2 || accounts[1].Label() != "cloudflare.work" {
		t.Fatalf("stored Accounts = %+v, %v", accounts, err)
	}
}

func TestTheAccountsPaneInsertsAnAccountWithTheRecordKeys(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newAccountHarness(t, s, newFake())

	// Outside the Accounts and Records panes, edit says where it acts;
	// insert and delete, in the Domains pane, Onboard and Offboard.
	h.press("e")
	h.assertShows("Accounts in the Accounts pane")
	h.assertHides("Add Account", "Edit Account", "Remove Account", "Unseal")

	h.press("d")
	h.assertShows("cannot Offboard a Zone")
	h.assertHides("Add Account", "Edit Account", "Remove Account", "Unseal")

	h.press("shift+tab", "insert")
	h.assertShows("Add Account", "Provider", "Name", "API token")

	h.press("esc", "delete")
	h.unsealWith(passphrase)
	h.assertShows("Remove Account", "cloudflare.work")
}

func TestAnAccountKeepsItsProviderWhenEdited(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newHarnessWith(t, s, tui.Deps{
		Providers:    dnsprovider.Registry{"cloudflare": newFake().Factory(), "godaddy": newFake().Factory()},
		UnsealMethod: fastPassphrase(),
	})

	h.press("shift+tab", "e")
	h.unsealWith(passphrase)

	// Left, Right and Enter do not change the Provider of an Account
	// being edited, nor open a list of Providers.
	h.press("up")
	h.assertShows("An Account keeps its Provider")
	h.press("right", "enter")
	h.typeText("2")
	h.press("down", "down", "enter")

	accounts, err := s.Accounts(context.Background())
	if err != nil || len(accounts) != 1 || accounts[0] != (dns.Account{ID: accounts[0].ID, Provider: "cloudflare", Name: "work2"}) {
		t.Fatalf("stored Accounts = %+v, %v", accounts, err)
	}
}

func TestTheAccountDialogMovesLikeTheRecordForm(t *testing.T) {
	s, _ := openStore(t)
	h := newHarnessWith(t, s, tui.Deps{
		Providers:    dnsprovider.Registry{"cloudflare": newFake().Factory(), "godaddy": newFake().Factory()},
		UnsealMethod: fastPassphrase(),
	})

	h.press("i")
	h.assertShows("‹ cloudflare ›", "The DNS hosting Provider", "Esc cancel")

	// Left and Right change the Provider; Enter lists every one.
	h.press("right")
	h.assertShows("‹ godaddy ›")
	h.press("enter", "up", "enter")
	h.assertShows("‹ cloudflare ›")

	// Up and Down move through the fields, each with its hint at the bottom.
	h.press("down")
	h.assertShows("<provider>.<name>")
	h.press("down")
	h.assertShows("seals it in its local database")
	h.press("down")
	h.assertShows("› [ Save ]", "discovers its Zones")
	h.press("down")
	h.assertShows("The DNS hosting Provider")
	h.press("up")

	// Save checks the fields, as Enter on the last one used to.
	h.press("enter")
	h.assertShows("Enter a name for the Account.")
}

func TestARenamedAccountIsUsedAtOnceNotOnlyAfterTheCacheReloads(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	f := workZones()
	h := newAccountHarness(t, s, f)

	h.press("shift+tab", "e")
	h.unsealWith(passphrase)
	h.press("backspace", "backspace", "backspace", "backspace")
	h.typeText("office")
	h.press("down", "down")

	// Save, but hold back the cache reload: the token is already sealed
	// for cloudflare.office, so a re-listing started now must use it.
	save := h.pressHolding("enter")
	m, _ := h.model.Update(save())
	h.model = m

	if got := h.relistToken(f); got != token {
		t.Fatalf("the API token = %q, want %q", got, token)
	}

	h.assertShows("Re-listed cloudflare.office")
	h.assertHides("decrypt", "authentication")
}

func TestAnAccountWithoutAnAPITokenCanBeRenamed(t *testing.T) {
	s, path := enrollFirstAccount(t)
	execSQL(t, path, `UPDATE accounts SET secret = NULL`)
	h := newAccountHarness(t, s, newFake())

	h.press("shift+tab", "e")
	h.unsealWith(passphrase)
	h.press("backspace", "backspace", "backspace", "backspace")
	h.typeText("office")
	h.press("down", "down", "enter")

	h.assertShows("Saved cloudflare.office.")

	if sealed, err := s.AccountSecret(context.Background(), 1); err != nil || sealed != nil {
		t.Fatalf("secret = %v, %v, want none", sealed, err)
	}
}

func TestTheEditAccountDialogKeepsOneSizeWhileSaving(t *testing.T) {
	s, _ := enrollFirstAccount(t)
	h := newAccountHarness(t, s, workZones())

	h.press("shift+tab", "e")
	h.unsealWith(passphrase)

	const title = "Edit Account cloudflare.work"

	w, ht := h.dialogSize(title)

	// The status line has no Provider to change or list.
	h.assertShows("Enter next or save")
	h.assertHides("change ·", "list or save")

	h.typeText("2")
	h.press("down", "down")
	h.pressHolding("enter")
	h.assertShows("saving…")

	if gw, gh := h.dialogSize(title); gw != w || gh != ht {
		t.Fatalf("saving resized the dialog from %dx%d to %dx%d:\n%s", w, ht, gw, gh, h.view())
	}
}
