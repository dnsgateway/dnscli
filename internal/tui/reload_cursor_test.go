package tui_test

import (
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
)

func TestCursorsStayOnTheirItemsWhenAReloadReordersTheLists(t *testing.T) {
	s, _ := openStore(t)
	seedCache(t, s)
	h := newAccountHarness(t, s, fake.New().AddZone(dns.Zone{Name: "aaa.example", ProviderID: "za1"}))

	// Domains: example.com, example.org, two.tv. Accounts: personal, work.
	h.press("j", "shift+tab", "j")
	h.assertShows("›example.org", "›cloudflare.work")

	// cloudflare.alpha and its aaa.example sort first in their lists.
	h.fillAddAccount("alpha", "alpha-token")
	h.typeText(passphrase)
	h.press("shift+tab")
	h.typeText(passphrase)
	h.press("enter")

	h.assertShows("aaa.example", "cloudflare.alpha", "›example.org", "›cloudflare.work")

	if !h.highlighted("cloudflare.work") {
		t.Fatalf("the Accounts cursor left cloudflare.work:\n%s", h.view())
	}
}
