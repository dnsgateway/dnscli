package tui_test

import (
	"context"
	"database/sql"
	"testing"
)

// execSQL changes the database behind the app's back, as another process
// or a damaged file would.
func execSQL(t *testing.T, path, query string) {
	t.Helper()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.ExecContext(context.Background(), query); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestAFailedReloadKeepsTheShownAccountsAndZones(t *testing.T) {
	s, path := openStore(t)
	seedCache(t, s)
	h := newAccountHarness(t, s, newFake())
	h.assertShows("example.com", "cloudflare.personal", "cloudflare.work")

	// A row the Store cannot read makes every later reload fail.
	execSQL(t, path, `INSERT INTO zones (account_id, name, refreshed_at) VALUES (1, 'bad.example', 'bogus')`)

	h.fillAddAccount("lab", "lab-token")
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")

	h.assertShows("example.com", "two.tv", "example.org", "cloudflare.personal", "cloudflare.work", "Could not reload")
}
