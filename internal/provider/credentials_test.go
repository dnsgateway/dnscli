// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package provider_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/dnsgateway/dnsprovider/providers"

	"github.com/sergelogvinov/dnscli/internal/provider"
)

func TestCredentialsReadASealedFieldMap(t *testing.T) {
	o := provider.NewOpener(providers.Registry())

	got, err := o.Credentials("namecheap", []byte(`{"user":"apiuser","key":"apikey","client_ip":"192.0.2.10"}`))

	want := map[string]string{"user": "apiuser", "key": "apikey", "client_ip": "192.0.2.10"}
	if err != nil || !maps.Equal(got, want) {
		t.Fatalf("Credentials = %v, %v; want %v", got, err, want)
	}
}

func TestCredentialsReadAnOldTokenIntoTheProvidersFields(t *testing.T) {
	o := provider.NewOpener(providers.Registry())

	for _, tc := range []struct {
		typ, token string
		want       map[string]string
	}{
		{"cloudflare", "cf-token", map[string]string{"token": "cf-token"}},
		{"godaddy", "gd-token", map[string]string{"token": "gd-token"}},
		{"namecheap", "apiuser:apikey:192.0.2.10", map[string]string{"user": "apiuser", "key": "apikey", "client_ip": "192.0.2.10"}},
	} {
		got, err := o.Credentials(tc.typ, []byte(tc.token))
		if err != nil || !maps.Equal(got, tc.want) {
			t.Errorf("%s: Credentials(%q) = %v, %v; want %v", tc.typ, tc.token, got, err, tc.want)
		}
	}
}

func TestEncodedCredentialsAreJSONAndReadBack(t *testing.T) {
	o := provider.NewOpener(providers.Registry())

	for typ, old := range map[string]string{
		"cloudflare": "cf-token",
		"godaddy":    "gd-token",
		"namecheap":  "apiuser:apikey:192.0.2.10",
	} {
		fields, err := o.Credentials(typ, []byte(old))
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}

		plain := provider.EncodeCredentials(fields)
		if !strings.HasPrefix(string(plain), "{") {
			t.Errorf("%s: encoded %q, want a JSON object", typ, plain)
		}

		again, err := o.Credentials(typ, plain)
		if err != nil || !maps.Equal(again, fields) {
			t.Errorf("%s: read back %v, %v; want %v", typ, again, err, fields)
		}
	}
}

func TestCredentialsOfAnUnknownProviderTypeAreRefused(t *testing.T) {
	if _, err := provider.NewOpener(providers.Registry()).Credentials("route53", []byte("tok")); err == nil {
		t.Error("Credentials(route53) succeeded, want an error")
	}
}
