package namecheap_test

// Seam 2: Apply against the scripted Namecheap XML API. setHosts replaces a
// domain's whole host list, so these tests check every host it received.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// zoneHosts is a Zone whose hosts include ones dnscli does not write: a
// URL redirect, a URL301, a FRAME and an ALIAS, with Namecheap's own
// MXPref on non-MX hosts and targets with trailing dots.
func zoneHosts() []ncHost {
	return []ncHost{
		{ID: "11", Name: "@", Type: "A", Address: "192.0.2.1", MXPref: 10, TTL: 1799},
		{ID: "12", Name: "www", Type: "CNAME", Address: "example.com.", MXPref: 10, TTL: 1800},
		{ID: "13", Name: "@", Type: "MX", Address: "mx1.example.net.", MXPref: 10, TTL: 3600},
		{ID: "14", Name: "go", Type: "URL", Address: "https://example.org/", MXPref: 10, TTL: 1800},
		{ID: "15", Name: "old", Type: "URL301", Address: "https://example.org/new", MXPref: 10, TTL: 1800},
		{ID: "16", Name: "frame", Type: "FRAME", Address: "https://example.org/", MXPref: 10, TTL: 1800},
		{ID: "17", Name: "alias", Type: "ALIAS", Address: "target.example.net.", MXPref: 10, TTL: 300},
	}
}

// zoneHostsSent are zoneHosts as setHosts must receive them back.
func zoneHostsSent() []sentHost {
	return []sentHost{
		{"@", "A", "192.0.2.1", "10", "1799"},
		{"www", "CNAME", "example.com.", "10", "1800"},
		{"@", "MX", "mx1.example.net.", "10", "3600"},
		{"go", "URL", "https://example.org/", "10", "1800"},
		{"old", "URL301", "https://example.org/new", "10", "1800"},
		{"frame", "FRAME", "https://example.org/", "10", "1800"},
		{"alias", "ALIAS", "target.example.net.", "10", "300"},
	}
}

// zoneAPI is a scripted API holding example.com with zoneHosts and a
// forwarding EmailType.
func zoneAPI() *ncAPI {
	return &ncAPI{
		hosts:     map[string][]ncHost{"example.com": zoneHosts()},
		emailType: map[string]string{"example.com": "FWD"},
	}
}

// apply applies changes to example.com, checking there is one result per
// change, in Changeset order.
func apply(t *testing.T, api *ncAPI, changes ...dns.Change) provider.ApplyResult {
	t.Helper()

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: changes})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(res.Results) != len(changes) {
		t.Fatalf("results = %+v, want one per change", res.Results)
	}

	for i, r := range res.Results {
		if !reflect.DeepEqual(r.Change, changes[i]) {
			t.Errorf("result %d is for %+v, want %+v", i, r.Change, changes[i])
		}
	}

	return res
}

// applyOK is apply, failing the test on any change's error.
func applyOK(t *testing.T, api *ncAPI, changes ...dns.Change) provider.ApplyResult {
	t.Helper()

	res := apply(t, api, changes...)

	for i, r := range res.Results {
		if r.Err != nil {
			t.Errorf("result %d: %v", i, r.Err)
		}
	}

	return res
}

// wantSetHosts checks that one setHosts was sent for example.com, after a
// getHosts, with these hosts and EmailType.
func wantSetHosts(t *testing.T, api *ncAPI, emailType string, hosts []sentHost) {
	t.Helper()

	if got, want := api.Commands(), []string{"namecheap.domains.dns.getHosts", "namecheap.domains.dns.setHosts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}

	set := api.setHostsSent()[0]
	if set.Get("SLD") != "example" || set.Get("TLD") != "com" {
		t.Errorf("setHosts SLD=%q TLD=%q, want example and com", set.Get("SLD"), set.Get("TLD"))
	}

	if got := set.Get("EmailType"); got != emailType {
		t.Errorf("setHosts EmailType = %q, want %q", got, emailType)
	}

	if got := sentHostFields(set); !reflect.DeepEqual(got, hosts) {
		t.Errorf("setHosts hosts =\n%v\nwant\n%v", got, hosts)
	}
}

func TestApplyAddsARecordKeepingEveryHostAndTheEmailType(t *testing.T) {
	api := zoneAPI()
	add := dns.Record{Type: "TXT", Name: "example.com", Value: "v=spf1 -all", TTL: 300}

	res := applyOK(t, api, dns.Change{Kind: dns.ChangeAdd, After: add})

	wantSetHosts(t, api, "FWD", append(zoneHostsSent(), sentHost{"@", "TXT", "v=spf1 -all", "", "300"}))

	if !reflect.DeepEqual(res.Results[0].Record, add) {
		t.Errorf("Record = %+v, want the Record as sent, %+v", res.Results[0].Record, add)
	}
}

func TestApplyModifiesARecordInPlaceKeepingEveryOtherHost(t *testing.T) {
	api := zoneAPI()
	before := dns.Record{ProviderID: "12", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 1800}
	after := dns.Record{ProviderID: "12", Type: "CNAME", Name: "www.example.com", Value: "shop.example.net", TTL: 600}

	res := applyOK(t, api, dns.Change{Kind: dns.ChangeModify, Before: before, After: after})

	want := zoneHostsSent()
	want[1] = sentHost{"www", "CNAME", "shop.example.net.", "", "600"}
	wantSetHosts(t, api, "FWD", want)

	// Namecheap gives every host a new HostId: the Refresh reads it.
	if got, want := res.Results[0].Record, (dns.Record{Type: "CNAME", Name: "www.example.com", Value: "shop.example.net", TTL: 600}); !reflect.DeepEqual(got, want) {
		t.Errorf("Record = %+v, want the Record as sent without its old HostId, %+v", got, want)
	}
}

func TestApplyDeletesARecordKeepingEveryOtherHost(t *testing.T) {
	api := zoneAPI()
	before := dns.Record{ProviderID: "11", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1799}

	res := applyOK(t, api, dns.Change{Kind: dns.ChangeDelete, Before: before})

	wantSetHosts(t, api, "FWD", zoneHostsSent()[1:])

	if !reflect.DeepEqual(res.Results[0].Record, dns.Record{}) {
		t.Errorf("Record = %+v, want none for a deletion", res.Results[0].Record)
	}
}

func TestApplyMatchesARecordWithoutAHostIDByTypeNameAndValue(t *testing.T) {
	api := zoneAPI()
	before := dns.Record{Type: "MX", Name: "example.com", Value: "10 mx1.example.net", TTL: 3600}

	applyOK(t, api, dns.Change{Kind: dns.ChangeDelete, Before: before})

	want := zoneHostsSent()
	wantSetHosts(t, api, "FWD", append(want[:2:2], want[3:]...))
}

func TestApplyMakesSeveralChangesInOneCall(t *testing.T) {
	api := zoneAPI()

	changes := []dns.Change{
		{Kind: dns.ChangeAdd, After: dns.Record{Type: "TXT", Name: "_dmarc.example.com", Value: "v=DMARC1; p=none", TTL: 1800}},
		{
			Kind:   dns.ChangeModify,
			Before: dns.Record{ProviderID: "13", Type: "MX", Name: "example.com", Value: "10 mx1.example.net", TTL: 3600},
			After:  dns.Record{Type: "MX", Name: "example.com", Value: "5 mx.example.org", TTL: 3600},
		},
		{Kind: dns.ChangeDelete, Before: dns.Record{ProviderID: "11", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1799}},
	}

	res := applyOK(t, api, changes...)

	sent := zoneHostsSent()
	wantSetHosts(t, api, "FWD", []sentHost{
		sent[1],
		{"@", "MX", "mx.example.org.", "5", "3600"},
		sent[3], sent[4], sent[5], sent[6],
		{"_dmarc", "TXT", "v=DMARC1; p=none", "", "1800"},
	})

	want := []dns.Record{changes[0].After, changes[1].After, {}}
	for i, r := range res.Results {
		if !reflect.DeepEqual(r.Record, want[i]) {
			t.Errorf("result %d: Record = %+v, want %+v", i, r.Record, want[i])
		}
	}
}

// wantAllFailed checks that every change failed with an error holding msg,
// and carries no Record.
func wantAllFailed(t *testing.T, res provider.ApplyResult, msg string, kind error) {
	t.Helper()

	for i, r := range res.Results {
		if r.Err == nil || !strings.Contains(r.Err.Error(), msg) {
			t.Errorf("result %d: err = %v, want it to hold %q", i, r.Err, msg)
		}

		if kind != nil && !errors.Is(r.Err, kind) {
			t.Errorf("result %d: err = %v, want %v", i, r.Err, kind)
		}

		if !reflect.DeepEqual(r.Record, dns.Record{}) {
			t.Errorf("result %d: Record = %+v, want none", i, r.Record)
		}
	}
}

// wantNoSetHosts checks that Apply sent no setHosts.
func wantNoSetHosts(t *testing.T, api *ncAPI) {
	t.Helper()

	if sent := api.setHostsSent(); len(sent) != 0 {
		t.Errorf("setHosts sent %d times, want never", len(sent))
	}
}

func TestApplyFailsWholeWhenARecordChangedAtNamecheap(t *testing.T) {
	add := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "A", Name: "new.example.com", Value: "192.0.2.9", TTL: 1800}}

	for _, c := range []struct {
		name   string
		before dns.Record
	}{
		{"gone HostId", dns.Record{ProviderID: "99", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1799}},
		{"HostId now holds another value", dns.Record{ProviderID: "11", Type: "A", Name: "example.com", Value: "192.0.2.7", TTL: 1799}},
		{"no HostId and no such value", dns.Record{Type: "MX", Name: "example.com", Value: "20 mx1.example.net", TTL: 3600}},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := zoneAPI()

			res := apply(t, api, add, dns.Change{Kind: dns.ChangeDelete, Before: c.before})

			wantAllFailed(t, res, c.before.Type+" "+c.before.Name+" "+c.before.Value, provider.ErrNotFound)
			wantNoSetHosts(t, api)
		})
	}
}

func TestApplyMatchesEachHostToOneChangeOnly(t *testing.T) {
	api := zoneAPI()
	before := dns.Record{Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1799}

	res := apply(t, api, dns.Change{Kind: dns.ChangeDelete, Before: before}, dns.Change{Kind: dns.ChangeDelete, Before: before})

	wantAllFailed(t, res, "A example.com 192.0.2.1", provider.ErrNotFound)
	wantNoSetHosts(t, api)
}

func TestApplyRefusesTypesItDoesNotWriteWithoutWriting(t *testing.T) {
	ok := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "A", Name: "new.example.com", Value: "192.0.2.9", TTL: 1800}}
	url := dns.Record{ProviderID: "14", Type: "URL", Name: "go.example.com", Value: "https://example.org/", TTL: 1800}

	for _, c := range []struct {
		name   string
		change dns.Change
		msg    string
	}{
		{"SRV", dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "SRV", Name: "_sip._tcp.example.com", Value: "10 5 5060 sip.example.net", TTL: 1800}}, "SRV"},
		{"URL redirect", dns.Change{Kind: dns.ChangeModify, Before: url, After: dns.Record{Type: "URL", Name: "go.example.com", Value: "https://example.net/", TTL: 1800}}, "URL"},
		{"ALIAS", dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "ALIAS", Name: "example.com", Value: "target.example.net", TTL: 300}}, "ALIAS"},
		{"PTR", dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "PTR", Name: "x.example.com", Value: "host.example.net", TTL: 300}}, "PTR"},
		{"NS at the apex", dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "NS", Name: "example.com", Value: "ns1.example.net", TTL: 1800}}, "NS"},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := zoneAPI()

			res := apply(t, api, ok, c.change)

			wantAllFailed(t, res, c.msg, nil)
			wantAllFailed(t, res, "namecheap", nil)
			wantNoSetHosts(t, api)
		})
	}
}

func TestApplyDeletesAURLRedirect(t *testing.T) {
	api := zoneAPI()
	before := dns.Record{ProviderID: "14", Type: "URL", Name: "go.example.com", Value: "https://example.org/", TTL: 1800}

	applyOK(t, api, dns.Change{Kind: dns.ChangeDelete, Before: before})

	want := zoneHostsSent()
	wantSetHosts(t, api, "FWD", append(want[:3:3], want[4:]...))
}

// changesForAll are an addition, a modification and a deletion of
// zoneHosts.
func changesForAll() []dns.Change {
	return []dns.Change{
		{Kind: dns.ChangeAdd, After: dns.Record{Type: "A", Name: "new.example.com", Value: "192.0.2.9", TTL: 1800}},
		{
			Kind:   dns.ChangeModify,
			Before: dns.Record{ProviderID: "12", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 1800},
			After:  dns.Record{Type: "CNAME", Name: "www.example.com", Value: "shop.example.net", TTL: 1800},
		},
		{Kind: dns.ChangeDelete, Before: dns.Record{ProviderID: "11", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1799}},
	}
}

func TestApplyFailsEveryChangeWithTheErrorOfARejectedWrite(t *testing.T) {
	api := zoneAPI()
	api.fail = func(command string) []ncError {
		if command == "namecheap.domains.dns.setHosts" {
			return []ncError{{2050900, "Unknown error when adding host"}}
		}

		return nil
	}

	res := apply(t, api, changesForAll()...)

	wantAllFailed(t, res, "2050900: Unknown error when adding host", nil)

	if got := len(api.setHostsSent()); got != 1 {
		t.Errorf("setHosts sent %d times, want once", got)
	}
}

func TestApplyFailsEveryChangeWhenTheWriteIsNotASuccess(t *testing.T) {
	api := zoneAPI()
	api.setHostsFalse = true

	res := apply(t, api, changesForAll()...)

	wantAllFailed(t, res, "namecheap", nil)
}

func TestApplyFailsEveryChangeWhenTheZoneCannotBeRead(t *testing.T) {
	api := zoneAPI()
	api.fail = func(command string) []ncError {
		if command == "namecheap.domains.dns.getHosts" {
			return []ncError{{500000, "Too many requests"}}
		}

		return nil
	}

	res := apply(t, api, changesForAll()...)

	wantAllFailed(t, res, "500000: Too many requests", provider.ErrRateLimited)
	wantNoSetHosts(t, api)
}

func TestApplyOfNoChangesSendsNothing(t *testing.T) {
	api := zoneAPI()

	apply(t, api)

	if got := api.Commands(); len(got) != 0 {
		t.Errorf("Apply sent %v, want nothing", got)
	}
}

func TestApplySendsRelativeNamesTheMXPriorityApartAndQualifiedTargets(t *testing.T) {
	api := &ncAPI{hosts: map[string][]ncHost{"example.com": nil}}

	applyOK(t, api,
		dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "MX", Name: "mail.example.com", Value: "20 mx2.example.net", TTL: 3600}},
		dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "CNAME", Name: "Shop.Example.com", Value: "shops.example.net", TTL: 600}},
		dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "NS", Name: "sub.example.com", Value: "ns1.example.net", TTL: 1800}},
		dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "AAAA", Name: "*.dev.example.com", Value: "2001:db8::1", TTL: 60}},
		dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "CAA", Name: "example.com", Value: `0 issue "letsencrypt.org"`, TTL: 1800}},
		dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "MX", Name: "example.com", Value: "0 .", TTL: 1800}},
	)

	wantSetHosts(t, api, "MX", []sentHost{
		{"mail", "MX", "mx2.example.net.", "20", "3600"},
		{"Shop", "CNAME", "shops.example.net.", "", "600"},
		{"sub", "NS", "ns1.example.net.", "", "1800"},
		{"*.dev", "AAAA", "2001:db8::1", "", "60"},
		{"@", "CAA", `0 issue "letsencrypt.org"`, "", "1800"},
		{"@", "MX", ".", "0", "1800"},
	})
}
