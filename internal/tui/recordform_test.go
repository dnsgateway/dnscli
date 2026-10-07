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

	h.press("right", "right", "right") // MX
	h.assertShows("‹ MX ›")
	h.assertHides("Proxied")

	h.press("left") // CNAME
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

	h.press("i", "right", "right") // CNAME
	h.press("tab")
	h.typeText("www")
	h.press("tab")
	h.typeText("work.example.")
	h.press("tab", "tab")
	h.assertShows("Proxied", "‹ off ›")

	h.press("right")
	h.assertShows("‹ on ›")
	h.saveForm()
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
	h.press("left")
	h.assertShows("‹ off ›")
	h.saveForm()
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

func TestThePreviewShowsAProxyOnlyChange(t *testing.T) {
	proxied := dns.Record{ProviderID: "rw3", Type: "CNAME", Name: "www.work.example", Value: "work.example", TTL: 300,
		Extras: map[string]string{"proxied": "true"}}
	f := fake.New().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial}, proxied)
	f.SetProxiableTypes("A", "AAAA", "CNAME")
	h := openValidatedShowing(t, f, "www")

	h.press("e", "tab", "tab", "tab", "tab", "left")
	h.saveForm()
	h.press("p")

	h.assertShows("~ www CNAME work.example TTL 300 proxied → www CNAME work.example TTL 300 not proxied")
}

// unproxiable is an A Record Cloudflare cannot proxy, such as one with a
// private address: it carries no proxy flag.
var unproxiable = dns.Record{ProviderID: "rw3", Type: "A", Name: "lan.work.example", Value: "10.0.0.1", TTL: 300}

func TestEditingARecordTheProviderCannotProxyHasNoProxyFlag(t *testing.T) {
	f := fake.New().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial}, unproxiable)
	f.SetProxiableTypes("A", "AAAA", "CNAME")
	h := openValidatedShowing(t, f, "10.0.0.1")

	h.press("e")
	h.assertShows("Edit Record", "‹ A ›")
	h.assertHides("Proxied")

	// Submitting it unchanged changes nothing.
	h.saveForm()
	h.assertHides("Edit Record", "pending change")

	// A changed TTL modifies only the TTL.
	h.press("e", "tab", "tab", "tab")
	h.clearText(len("300"))
	h.typeText("600")
	h.saveForm()
	h.assertShows("1 pending change")

	h.press("p", "enter")

	calls := applyCalls(f)
	if len(calls) != 1 || len(calls[0].Changes) != 1 {
		t.Fatalf("Apply calls = %+v", calls)
	}

	if got := calls[0].Changes[0].After.Extras; len(got) != 0 {
		t.Fatalf("applied extras = %v, want none", got)
	}
}

func TestAddingARecordWithTheProxyFlagOffSendsNoFlag(t *testing.T) {
	f := proxyingZones()
	h := openValidated(t, f)

	h.addRecord("A", "lan", "10.0.0.1", "300")
	h.assertHides("Add Record")

	h.press("p", "enter")

	calls := applyCalls(f)
	if len(calls) != 1 || len(calls[0].Changes) != 1 {
		t.Fatalf("Apply calls = %+v", calls)
	}

	if got := calls[0].Changes[0].After.Extras; len(got) != 0 {
		t.Fatalf("applied extras = %v, want none: the user left the proxy off", got)
	}
}

func TestARecordWithoutTheProxyFlagIsTheSameAsOneWithItOff(t *testing.T) {
	f := fake.New().AddZone(dns.Zone{Name: "work.example", ProviderID: "zw1", Serial: liveSerial}, unproxiable)
	f.SetProxiableTypes("A", "AAAA", "CNAME")
	h := openValidatedShowing(t, f, "10.0.0.1")

	h.press("e", "tab", "tab", "tab")
	h.clearText(len("300"))
	h.typeText("600")
	h.saveForm()

	// The Provider now reports the flag, off, on the same Record.
	off := unproxiable
	off.Extras = map[string]string{"proxied": "false"}
	f.SetRecords("zw1", off)

	h.press("p", "enter")

	if calls := applyCalls(f); len(calls) != 1 {
		t.Fatalf("Apply calls = %+v, want the Changeset sent without a conflict", calls)
	}

	h.assertShows("Applied 1 change to work.example.")
}

func TestUpAndDownMoveBetweenTheRecordFormsFieldsAndLeftAndRightChangeAnOption(t *testing.T) {
	h := openValidated(t, refreshZones())

	h.press("i")
	h.assertShows("› Type", "‹ A ›")

	h.press("right", "right")
	h.assertShows("‹ CNAME ›")
	h.press("left")
	h.assertShows("‹ AAAA ›")
	h.press("left", "left") // round to the last type
	h.assertShows("‹ URI ›")

	h.press("down")
	h.assertShows("› Name")
	h.press("down", "down", "down")
	h.assertShows("› [ Save ]")
	h.press("up")
	h.assertShows("› TTL")
	h.press("down", "down") // round to the first field
	h.assertShows("› Type")
}

func TestEnterOnAnOptionListsItsValuesToPickOneWithUpAndDown(t *testing.T) {
	h := openValidated(t, refreshZones())

	h.press("i", "enter")
	h.assertShows("› Type", "AAAA", "CNAME", "MX", "SRV", "CAA", "URI")
	h.assertPicked("A")

	h.press("down", "down", "down")
	h.assertPicked("MX")
	h.press("up")
	h.assertPicked("CNAME")

	h.press("enter")
	h.assertShows("Add Record", "› Type", "‹ CNAME ›")
	h.assertHides("SRV")

	// Esc closes the list and keeps the type.
	h.press("enter", "down", "esc")
	h.assertShows("Add Record", "‹ CNAME ›")
	h.assertHides("SRV")

	// Enter on a text field moves on; only Save stages the Record.
	h.press("down")
	h.typeText("www")
	h.press("enter")
	h.assertShows("› Value")
	h.typeText("work.example.")
	h.press("enter", "enter")
	h.assertShows("Add Record", "› [ Save ]")

	h.press("enter")
	h.assertHides("Add Record")
	h.assertShows("+ www.work.example", "1 pending change")
}

// assertPicked checks that the open picker highlights option.
func (h *harness) assertPicked(option string) {
	h.t.Helper()

	if !h.highlighted("› " + option + " ") {
		h.t.Fatalf("the picker does not highlight %q:\n%s", option, h.view())
	}
}
