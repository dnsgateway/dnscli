package tui_test

import (
	"reflect"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider/fake"
)

// proxyingZones is refreshZones at a Provider that proxies A, AAAA and
// CNAME Records, as Cloudflare does.
func proxyingZones() *fake.Provider {
	f := refreshZones()
	f.SetProxiableTypes("A", "AAAA", "CNAME")

	return f
}

func TestTheRecordFormShowsTheProxyFlagOnlyForTypesTheProviderProxies(t *testing.T) {
	h := openValidated(t, proxyingZones())

	h.press("i")
	h.assertShows("Add Record", "‹ A ›", "Proxied")

	h.press("down", "down", "down") // MX
	h.assertShows("‹ MX ›")
	h.assertHides("Proxied")

	h.press("up") // CNAME
	h.assertShows("‹ CNAME ›", "Proxied")
}

func TestTheRecordFormHasNoProxyFlagWhenTheProviderHasNone(t *testing.T) {
	h := openValidated(t, refreshZones())

	h.press("i")
	h.assertShows("Add Record", "‹ A ›")
	h.assertHides("Proxied")
}

func TestAddingAProxiedRecord(t *testing.T) {
	f := proxyingZones()
	h := openValidated(t, f)

	h.press("i", "down", "down") // CNAME
	h.press("tab")
	h.typeText("www")
	h.press("tab")
	h.typeText("work.example.")
	h.press("tab", "tab")
	h.assertShows("Proxied", "‹ off ›")

	h.press("down")
	h.assertShows("‹ on ›")
	h.press("enter")
	h.assertHides("Add Record")

	h.press("p", "enter")

	calls := applyCalls(f)
	if len(calls) != 1 || len(calls[0].Changes) != 1 {
		t.Fatalf("Apply calls = %+v", calls)
	}

	if got, want := calls[0].Changes[0].After.Extras, map[string]string{"proxied": "true"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("applied extras = %v, want %v", got, want)
	}
}

func TestEditingARecordShowsAndChangesItsProxyFlag(t *testing.T) {
	proxied := dns.Record{ProviderID: "rw3", Type: "CNAME", Name: "www.work.example", Value: "work.example", TTL: 300,
		Extras: map[string]string{"proxied": "true", "comment": "kept"}}
	f := fake.New().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial}, proxied)
	f.SetProxiableTypes("A", "AAAA", "CNAME")
	h := openValidatedShowing(t, f, "www")

	h.press("e")
	h.assertShows("Edit Record", "Proxied", "on")

	h.press("tab", "tab", "tab", "tab")
	h.assertShows("‹ on ›")
	h.press("up")
	h.assertShows("‹ off ›")
	h.press("enter")
	h.assertShows("~ www")

	h.press("p", "enter")

	calls := applyCalls(f)
	if len(calls) != 1 || len(calls[0].Changes) != 1 {
		t.Fatalf("Apply calls = %+v", calls)
	}

	if got, want := calls[0].Changes[0].After.Extras, map[string]string{"proxied": "false", "comment": "kept"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("applied extras = %v, want %v", got, want)
	}
}
