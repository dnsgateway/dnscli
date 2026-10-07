package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
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
			opts, err := parseOptions(tc.args, env(tc.env), tc.goos, io.Discard)
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

func TestTheDatabasePathComesFromTheDBFlag(t *testing.T) {
	opts, err := parseOptions([]string{"--db", "/tmp/x.db"}, func(string) string { return "" }, "linux", io.Discard)
	if err != nil || opts.dbPath != "/tmp/x.db" {
		t.Fatalf("parseOptions = %+v, %v", opts, err)
	}
}

func TestAnInvalidCommandLinePrintsUsageAndExitsWithStatus2(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"-db", "x.db"}, {"extra"}, {"--unseal", "yubikey"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stderr bytes.Buffer

			if code := runMain(args, func(string) string { return "" }, "linux", &stderr); code != 2 {
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

	if code := runMain([]string{"--help"}, func(string) string { return "" }, "linux", &stderr); code != 0 {
		t.Fatalf("exit status = %d, want 0", code)
	}

	if !strings.Contains(stderr.String(), "--unseal") {
		t.Fatalf("help does not show usage:\n%s", stderr.String())
	}
}
