// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package resolver

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// writeResolvConf writes a resolv.conf naming servers, with the given mtime.
func writeResolvConf(t *testing.T, path string, mtime time.Time, servers ...string) {
	t.Helper()

	var conf string
	for _, s := range servers {
		conf += "nameserver " + s + "\n"
	}

	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func assertServers(t *testing.T, s *System, want ...string) {
	t.Helper()

	config, err := s.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !slices.Equal(config.Servers, want) {
		t.Fatalf("servers = %v, want %v", config.Servers, want)
	}
}

func TestAnEditedResolvConfIsReadAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	writeResolvConf(t, path, start, "192.0.2.1")

	s := &System{Path: path}
	assertServers(t, s, "192.0.2.1")

	// The network changed, e.g. after joining another Wi-Fi.
	writeResolvConf(t, path, start.Add(time.Minute), "192.0.2.2")
	assertServers(t, s, "192.0.2.2")
}

func TestAMissingResolvConfIsReadOnceItAppears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	s := &System{Path: path}

	if _, err := s.load(); err == nil {
		t.Fatal("load succeeded without a resolv.conf")
	}

	writeResolvConf(t, path, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC), "192.0.2.1")
	assertServers(t, s, "192.0.2.1")
}
