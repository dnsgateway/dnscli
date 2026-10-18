// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/sergelogvinov/dnscli/internal/vault"
)

func TestTheUnsealMethodComesFromTheFlagThenTheEnvironment(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == unsealEnv {
				return v
			}

			return ""
		}
	}

	for _, tc := range []struct {
		name    string
		args    []string
		env     string
		goos    string
		wantErr bool
	}{
		{name: "default outside macOS", goos: "linux"},
		{name: "environment", env: "passphrase", goos: "linux"},
		{name: "flag", args: []string{"--unseal", "passphrase"}, goos: "linux"},
		{name: "flag beats environment", args: []string{"--unseal=passphrase"}, env: "keychain", goos: "linux"},
		{name: "unknown in environment", env: "yubikey", goos: "linux", wantErr: true},
		{name: "unknown flag value", args: []string{"--unseal", "yubikey"}, goos: "linux", wantErr: true},
		{name: "single-dash long flag is refused", args: []string{"-db", "x.db"}, goos: "linux", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseOptions(tc.args, env(tc.env), tc.goos, unsealMethods(nil), io.Discard)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseOptions succeeded, want an error")
				}

				return
			}

			if err != nil {
				t.Fatalf("parseOptions: %v", err)
			}

			if opts.unseal == nil || opts.unseal.Name() != "passphrase" {
				t.Fatalf("Unseal Method = %v, want passphrase", opts.unseal)
			}
		})
	}
}

// fakeKeychain stands in for the macOS Keychain; option parsing never uses it.
type fakeKeychain struct{}

func (fakeKeychain) Store([]byte) ([]byte, error) { return nil, errors.New("unused") }
func (fakeKeychain) Load([]byte) ([]byte, error)  { return nil, errors.New("unused") }
func (fakeKeychain) Delete([]byte) error          { return errors.New("unused") }

func TestTheKeychainIsTheDefaultUnsealMethodOnMacOS(t *testing.T) {
	withKeychain := unsealMethods(fakeKeychain{})

	for _, tc := range []struct {
		name    string
		args    []string
		env     string
		goos    string
		methods map[string]vault.Method
		want    string
	}{
		{name: "macOS", goos: "darwin", methods: withKeychain, want: "keychain"},
		{name: "macOS, passphrase by flag", args: []string{"--unseal", "passphrase"}, goos: "darwin", methods: withKeychain, want: "passphrase"},
		{name: "macOS, passphrase by environment", env: "passphrase", goos: "darwin", methods: withKeychain, want: "passphrase"},
		{name: "macOS build without the Keychain", goos: "darwin", methods: unsealMethods(nil), want: "passphrase"},
		{name: "Linux, even with a Keychain", goos: "linux", methods: withKeychain, want: "passphrase"},
		{name: "keychain chosen explicitly", env: "keychain", goos: "linux", methods: withKeychain, want: "keychain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string {
				if k == unsealEnv {
					return tc.env
				}

				return ""
			}

			opts, err := parseOptions(tc.args, getenv, tc.goos, tc.methods, io.Discard)
			if err != nil {
				t.Fatalf("parseOptions: %v", err)
			}

			if opts.unseal.Name() != tc.want {
				t.Fatalf("Unseal Method = %s, want %s", opts.unseal.Name(), tc.want)
			}

			if _, has := tc.methods["keychain"]; has != (opts.keychain != nil) {
				t.Fatalf("Keychain offered for enrolling = %v, want %v", opts.keychain != nil, has)
			}
		})
	}
}

func TestWithoutAKeychainItCannotBeChosenOrEnrolled(t *testing.T) {
	var stderr bytes.Buffer

	opts, err := parseOptions([]string{"--unseal", "keychain"}, func(string) string { return "" }, "darwin", unsealMethods(nil), &stderr)
	if err == nil || !strings.Contains(stderr.String(), "not available") {
		t.Fatalf("parseOptions = %v; stderr:\n%s", err, stderr.String())
	}

	opts, err = parseOptions(nil, func(string) string { return "" }, "linux", unsealMethods(nil), io.Discard)
	if err != nil || opts.keychain != nil {
		t.Fatalf("parseOptions = %+v, %v; want no Keychain to enroll", opts, err)
	}
}

func TestTheDatabasePathComesFromTheDBFlag(t *testing.T) {
	opts, err := parseOptions([]string{"--db", "/tmp/x.db"}, func(string) string { return "" }, "linux", unsealMethods(nil), io.Discard)
	if err != nil || opts.dbPath != "/tmp/x.db" {
		t.Fatalf("parseOptions = %+v, %v", opts, err)
	}
}

func TestSerialCheckConcurrencyComesFromTheFlag(t *testing.T) {
	getenv := func(string) string { return "" }

	opts, err := parseOptions(nil, getenv, "linux", unsealMethods(nil), io.Discard)
	if err != nil || opts.serialChecks != 8 {
		t.Fatalf("default: parseOptions = %+v, %v; want 8 Serial Checks", opts, err)
	}

	opts, err = parseOptions([]string{"--serial-checks", "3"}, getenv, "linux", unsealMethods(nil), io.Discard)
	if err != nil || opts.serialChecks != 3 {
		t.Fatalf("flag: parseOptions = %+v, %v; want 3 Serial Checks", opts, err)
	}
}

func TestAnInvalidCommandLinePrintsUsageAndExitsWithStatus2(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"-db", "x.db"}, {"extra"}, {"--unseal", "yubikey"}, {"--serial-checks", "0"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stderr bytes.Buffer

			if code := runMain(args, func(string) string { return "" }, "linux", unsealMethods(nil), &stderr); code != 2 {
				t.Fatalf("exit status = %d, want 2", code)
			}

			if out := stderr.String(); !strings.Contains(out, "dnscli: ") || !strings.Contains(out, "Usage:") || !strings.Contains(out, "--db") {
				t.Fatalf("stderr does not show the error and usage:\n%s", out)
			}
		})
	}
}

func TestHelpPrintsUsageAndExitsWithStatus0(t *testing.T) {
	var stderr bytes.Buffer

	if code := runMain([]string{"--help"}, func(string) string { return "" }, "linux", unsealMethods(nil), &stderr); code != 0 {
		t.Fatalf("exit status = %d, want 0", code)
	}

	if !strings.Contains(stderr.String(), "--unseal") {
		t.Fatalf("help does not show usage:\n%s", stderr.String())
	}
}

func TestTheCAFilesComeFromRepeatedFlagsOrTheEnvironmentPathList(t *testing.T) {
	sep := string(os.PathListSeparator)

	for _, tc := range []struct {
		name string
		args []string
		env  string
		want []string
	}{
		{name: "none"},
		{name: "repeated flag", args: []string{"--ca-file", "a.pem", "--ca-file", "b.pem"}, want: []string{"a.pem", "b.pem"}},
		{name: "environment path list", env: "a.pem" + sep + sep + "b.pem", want: []string{"a.pem", "b.pem"}},
		{name: "flag replaces environment", args: []string{"--ca-file", "c.pem"}, env: "a.pem" + sep + "b.pem", want: []string{"c.pem"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string {
				if k == caFileEnv {
					return tc.env
				}

				return ""
			}

			opts, err := parseOptions(tc.args, getenv, "linux", unsealMethods(nil), io.Discard)
			if err != nil {
				t.Fatalf("parseOptions: %v", err)
			}

			if !slices.Equal(opts.caFiles, tc.want) {
				t.Fatalf("CA files = %q, want %q", opts.caFiles, tc.want)
			}
		})
	}
}
