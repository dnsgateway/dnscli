package namecheap_test

// Seam 2: Apply against the scripted Namecheap XML API. setHosts replaces a
// domain's whole host list, so these tests check every host it received.

import (
	"context"
	"errors"
	"reflect"
	"slices"
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

	if want := (dns.Record{ProviderID: "TXT example.com 300 v=spf1 -all", Type: "TXT", Name: "example.com", Value: "v=spf1 -all", TTL: 300}); !reflect.DeepEqual(res.Results[0].Record, want) {
		t.Errorf("Record = %+v, want the Record as sent, with its ID, %+v", res.Results[0].Record, want)
	}
}

func TestApplyModifiesARecordInPlaceKeepingEveryOtherHost(t *testing.T) {
	api := zoneAPI()
	before := dns.Record{ProviderID: "CNAME www.example.com 1800 example.com", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 1800}
	after := dns.Record{Type: "CNAME", Name: "www.example.com", Value: "shop.example.net", TTL: 600}

	res := applyOK(t, api, dns.Change{Kind: dns.ChangeModify, Before: before, After: after})

	want := zoneHostsSent()
	want[1] = sentHost{"www", "CNAME", "shop.example.net.", "", "600"}
	wantSetHosts(t, api, "FWD", want)

	stored := dns.Record{ProviderID: "CNAME www.example.com 600 shop.example.net", Type: "CNAME", Name: "www.example.com", Value: "shop.example.net", TTL: 600}
	if got := res.Results[0].Record; !reflect.DeepEqual(got, stored) {
		t.Errorf("Record = %+v, want the Record as sent, with its new ID, %+v", got, stored)
	}
}

func TestApplyDeletesARecordKeepingEveryOtherHost(t *testing.T) {
	api := zoneAPI()
	before := dns.Record{ProviderID: "A example.com 1799 192.0.2.1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1799}

	res := applyOK(t, api, dns.Change{Kind: dns.ChangeDelete, Before: before})

	wantSetHosts(t, api, "FWD", zoneHostsSent()[1:])

	if !reflect.DeepEqual(res.Results[0].Record, dns.Record{}) {
		t.Errorf("Record = %+v, want none for a deletion", res.Results[0].Record)
	}
}

func TestApplyMatchesARecordWithoutAnIDByTypeNameAndValue(t *testing.T) {
	api := zoneAPI()
	before := dns.Record{Type: "MX", Name: "example.com", Value: "10 mx1.example.net", TTL: 3600}

	applyOK(t, api, dns.Change{Kind: dns.ChangeDelete, Before: before})

	want := zoneHostsSent()
	wantSetHosts(t, api, "FWD", append(want[:2:2], want[3:]...))
}

func TestApplyMakesSeveralChangesInOneCall(t *testing.T) {
	api := zoneAPI()
	api.emailType["example.com"] = "MX"

	changes := []dns.Change{
		{Kind: dns.ChangeAdd, After: dns.Record{Type: "TXT", Name: "_dmarc.example.com", Value: "v=DMARC1; p=none", TTL: 1800}},
		{
			Kind:   dns.ChangeModify,
			Before: dns.Record{ProviderID: "MX example.com 3600 10 mx1.example.net", Type: "MX", Name: "example.com", Value: "10 mx1.example.net", TTL: 3600},
			After:  dns.Record{Type: "MX", Name: "example.com", Value: "5 mx.example.org", TTL: 3600},
		},
		{Kind: dns.ChangeDelete, Before: dns.Record{ProviderID: "A example.com 1799 192.0.2.1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1799}},
	}

	res := applyOK(t, api, changes...)

	sent := zoneHostsSent()
	wantSetHosts(t, api, "MX", []sentHost{
		sent[1],
		{"@", "MX", "mx.example.org.", "5", "3600"},
		sent[3], sent[4], sent[5], sent[6],
		{"_dmarc", "TXT", "v=DMARC1; p=none", "", "1800"},
	})

	want := []dns.Record{
		{ProviderID: "TXT _dmarc.example.com 1800 v=DMARC1; p=none", Type: "TXT", Name: "_dmarc.example.com", Value: "v=DMARC1; p=none", TTL: 1800},
		{ProviderID: "MX example.com 3600 5 mx.example.org", Type: "MX", Name: "example.com", Value: "5 mx.example.org", TTL: 3600},
		{},
	}
	for i, r := range res.Results {
		if !reflect.DeepEqual(r.Record, want[i]) {
			t.Errorf("result %d: Record = %+v, want %+v", i, r.Record, want[i])
		}
	}
}

// applyFails applies changes to example.com, checking that Apply fails as
// a whole: nothing was applied, so it returns an error of its own (the TUI
// then says it could not apply the changes and keeps them all pending)
// holding msg, and kind when not nil.
func applyFails(t *testing.T, api *ncAPI, msg string, kind error, changes ...dns.Change) {
	t.Helper()

	res, err := newProvider(t, api).Apply(context.Background(), dns.Changeset{Zone: example, Changes: changes})
	if err == nil {
		t.Fatalf("Apply = %+v, nil; want an error holding %q", res, msg)
	}

	if !strings.Contains(err.Error(), msg) {
		t.Errorf("err = %v, want it to hold %q", err, msg)
	}

	if !strings.HasPrefix(err.Error(), "namecheap: ") {
		t.Errorf("err = %v, want it to name namecheap", err)
	}

	if kind != nil && !errors.Is(err, kind) {
		t.Errorf("err = %v, want %v", err, kind)
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
		{"TTL changed", dns.Record{ProviderID: "A example.com 300 192.0.2.1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 300}},
		{"value changed", dns.Record{ProviderID: "A example.com 1799 192.0.2.7", Type: "A", Name: "example.com", Value: "192.0.2.7", TTL: 1799}},
		{"no ID and no such value", dns.Record{Type: "MX", Name: "example.com", Value: "20 mx1.example.net", TTL: 3600}},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := zoneAPI()

			applyFails(t, api, c.before.Type+" "+c.before.Name+" "+c.before.Value, provider.ErrNotFound,
				add, dns.Change{Kind: dns.ChangeDelete, Before: c.before})
			wantNoSetHosts(t, api)
		})
	}
}

func TestApplyMatchesEachHostToOneChangeOnly(t *testing.T) {
	api := zoneAPI()
	before := dns.Record{Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1799}

	applyFails(t, api, "A example.com 192.0.2.1", provider.ErrNotFound, dns.Change{Kind: dns.ChangeDelete, Before: before}, dns.Change{Kind: dns.ChangeDelete, Before: before})
	wantNoSetHosts(t, api)
}

func TestApplyRefusesTypesItDoesNotWriteWithoutWriting(t *testing.T) {
	ok := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "A", Name: "new.example.com", Value: "192.0.2.9", TTL: 1800}}
	url := dns.Record{ProviderID: "URL go.example.com 1800 https://example.org/", Type: "URL", Name: "go.example.com", Value: "https://example.org/", TTL: 1800}
	caa := dns.Record{Type: "CAA", Name: "example.com", Value: `0 issue "letsencrypt.org"`, TTL: 1800}

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
		{"CAA added", dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "CAA", Name: "example.com", Value: `0 issue "letsencrypt.org"`, TTL: 1800}}, "CAA"},
		{"CAA modified", dns.Change{Kind: dns.ChangeModify, Before: caa, After: dns.Record{Type: "CAA", Name: "example.com", Value: `0 issue "pki.goog"`, TTL: 1800}}, "CAA"},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := zoneAPI()

			applyFails(t, api, c.msg, nil, ok, c.change)

			if got := api.Commands(); len(got) != 0 {
				t.Errorf("Apply sent %v, want nothing", got)
			}
		})
	}
}

func TestApplyDeletesAURLRedirect(t *testing.T) {
	api := zoneAPI()
	before := dns.Record{ProviderID: "URL go.example.com 1800 https://example.org/", Type: "URL", Name: "go.example.com", Value: "https://example.org/", TTL: 1800}

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
			Before: dns.Record{ProviderID: "CNAME www.example.com 1800 example.com", Type: "CNAME", Name: "www.example.com", Value: "example.com", TTL: 1800},
			After:  dns.Record{Type: "CNAME", Name: "www.example.com", Value: "shop.example.net", TTL: 1800},
		},
		{Kind: dns.ChangeDelete, Before: dns.Record{ProviderID: "A example.com 1799 192.0.2.1", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1799}},
	}
}

func TestApplyFailsWholeWithTheErrorOfARejectedWrite(t *testing.T) {
	api := zoneAPI()
	api.fail = func(command string) []ncError {
		if command == "namecheap.domains.dns.setHosts" {
			return []ncError{{2050900, "Unknown error when adding host"}}
		}

		return nil
	}

	applyFails(t, api, "2050900: Unknown error when adding host", nil, changesForAll()...)

	if got := len(api.setHostsSent()); got != 1 {
		t.Errorf("setHosts sent %d times, want once", got)
	}
}

func TestApplyFailsWholeWhenTheWriteIsNotAuthorized(t *testing.T) {
	api := zoneAPI()
	api.fail = func(command string) []ncError {
		if command == "namecheap.domains.dns.setHosts" {
			return []ncError{{1011102, "Parameter APIKey is invalid"}}
		}

		return nil
	}

	applyFails(t, api, "1011102: Parameter APIKey is invalid", provider.ErrUnauthorized, changesForAll()...)
}

func TestApplyFailsWholeWhenTheWriteIsNotASuccess(t *testing.T) {
	api := zoneAPI()
	api.setHostsFalse = true

	applyFails(t, api, "Refresh", nil, changesForAll()...)
}

func TestApplyFailsWholeWhenTheZoneCannotBeRead(t *testing.T) {
	api := zoneAPI()
	api.fail = func(command string) []ncError {
		if command == "namecheap.domains.dns.getHosts" {
			return []ncError{{500000, "Too many requests"}}
		}

		return nil
	}

	applyFails(t, api, "500000: Too many requests", provider.ErrRateLimited, changesForAll()...)
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
		dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "MX", Name: "example.com", Value: "0 .", TTL: 1800}},
	)

	wantSetHosts(t, api, "MX", []sentHost{
		{"mail", "MX", "mx2.example.net.", "20", "3600"},
		{"Shop", "CNAME", "shops.example.net.", "", "600"},
		{"sub", "NS", "ns1.example.net.", "", "1800"},
		{"*.dev", "AAAA", "2001:db8::1", "", "60"},
		{"@", "MX", ".", "0", "1800"},
	})
}

// records reads example.com's Records.
func records(t *testing.T, p provider.Provider) []dns.Record {
	t.Helper()

	set, err := p.Records(context.Background(), example)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	return set.Records
}

func TestRecordsKeepTheirIDsAcrossAnApplyThatRenumbersEveryHost(t *testing.T) {
	api := zoneAPI()
	p := newProvider(t, api)
	cached := records(t, p)

	// A deletion alone: setHosts gives every other host a new HostId.
	if _, err := p.Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeDelete, Before: cached[0]},
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := records(t, p); !reflect.DeepEqual(got, cached[1:]) {
		t.Fatalf("Records after the deletion =\n%v\nwant the cached ones, IDs included\n%v", got, cached[1:])
	}

	// The next Changeset, built from the cache, finds its Records.
	www := cached[1]
	after := www
	after.Value, after.ProviderID = "shop.example.net", ""

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeModify, Before: www, After: after},
		{Kind: dns.ChangeDelete, Before: cached[2]},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for i, r := range res.Results {
		if r.Err != nil {
			t.Errorf("result %d: %v", i, r.Err)
		}
	}
}

func TestAppliedRecordsAreAsTheNextReadHasThemIDsIncluded(t *testing.T) {
	api := zoneAPI()
	p := newProvider(t, api)
	www := records(t, p)[1]

	modified := www
	modified.Value, modified.TTL, modified.ProviderID = "shop.example.net", 600, ""

	res, err := p.Apply(context.Background(), dns.Changeset{Zone: example, Changes: []dns.Change{
		{Kind: dns.ChangeAdd, After: dns.Record{Type: "A", Name: "New.example.com", Value: "192.0.2.9", TTL: 1800}},
		{Kind: dns.ChangeModify, Before: www, After: modified},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	want := []dns.Record{
		{ProviderID: "A new.example.com 1800 192.0.2.9", Type: "A", Name: "New.example.com", Value: "192.0.2.9", TTL: 1800},
		{ProviderID: "CNAME www.example.com 600 shop.example.net", Type: "CNAME", Name: "www.example.com", Value: "shop.example.net", TTL: 600},
	}

	read := records(t, p)

	for i, r := range res.Results {
		if r.Err != nil {
			t.Errorf("result %d: %v", i, r.Err)
		}

		if !reflect.DeepEqual(r.Record, want[i]) {
			t.Errorf("result %d: Record = %+v, want %+v", i, r.Record, want[i])
		}

		if !slices.ContainsFunc(read, func(got dns.Record) bool { return reflect.DeepEqual(got, r.Record) }) {
			t.Errorf("result %d: Record %+v is not among the Records read next, %+v", i, r.Record, read)
		}
	}
}

func TestApplyDeletesACAARecord(t *testing.T) {
	api := zoneAPI()
	api.hosts["example.com"] = append(api.hosts["example.com"], ncHost{ID: "18", Name: "@", Type: "CAA", Address: `0 issue "letsencrypt.org"`, TTL: 1800})
	before := dns.Record{ProviderID: `CAA example.com 1800 0 issue "letsencrypt.org"`, Type: "CAA", Name: "example.com", Value: `0 issue "letsencrypt.org"`, TTL: 1800}

	applyOK(t, api, dns.Change{Kind: dns.ChangeDelete, Before: before})

	wantSetHosts(t, api, "FWD", zoneHostsSent())
}

func TestApplyRefusesMXWritesUnlessTheMailSettingIsCustomMX(t *testing.T) {
	mx := dns.Record{ProviderID: "MX example.com 3600 10 mx1.example.net", Type: "MX", Name: "example.com", Value: "10 mx1.example.net", TTL: 3600}
	ok := dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "A", Name: "new.example.com", Value: "192.0.2.9", TTL: 1800}}

	for _, emailType := range []string{"FWD", "MXE", "OX", ""} {
		for _, c := range []struct {
			name   string
			change dns.Change
		}{
			{"added", dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "MX", Name: "example.com", Value: "20 mx2.example.net", TTL: 3600}}},
			{"modified", dns.Change{Kind: dns.ChangeModify, Before: mx, After: dns.Record{Type: "MX", Name: "example.com", Value: "5 mx1.example.net", TTL: 3600}}},
		} {
			t.Run(emailType+" "+c.name, func(t *testing.T) {
				api := zoneAPI()
				api.emailType["example.com"] = emailType

				applyFails(t, api, `"Custom MX"`, nil, ok, c.change)

				if got, want := api.Commands(), []string{"namecheap.domains.dns.getHosts"}; !reflect.DeepEqual(got, want) {
					t.Errorf("requests = %v, want %v", got, want)
				}
			})
		}
	}
}

func TestApplyRefusesAnMXValueThatIsNotPriorityAndHost(t *testing.T) {
	for _, value := range []string{"10", "10 mx.example.net extra", "ten mx.example.net", "70000 mx.example.net", ""} {
		t.Run(value, func(t *testing.T) {
			api := &ncAPI{hosts: map[string][]ncHost{"example.com": nil}}

			applyFails(t, api, "priority host", nil, dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "MX", Name: "example.com", Value: value, TTL: 3600}})
			wantNoSetHosts(t, api)
		})
	}
}

func TestApplySplitsAnMXValueOnAnyWhitespace(t *testing.T) {
	api := &ncAPI{hosts: map[string][]ncHost{"example.com": nil}}

	applyOK(t, api, dns.Change{Kind: dns.ChangeAdd, After: dns.Record{Type: "MX", Name: "example.com", Value: "10\t mx.example.net", TTL: 3600}})

	wantSetHosts(t, api, "MX", []sentHost{{"@", "MX", "mx.example.net.", "10", "3600"}})
}
