package main

import (
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
			opts, err := parseOptions(tc.args, env(tc.env), tc.goos)
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
	opts, err := parseOptions([]string{"--db", "/tmp/x.db"}, func(string) string { return "" }, "linux")
	if err != nil || opts.dbPath != "/tmp/x.db" {
		t.Fatalf("parseOptions = %+v, %v", opts, err)
	}
}
