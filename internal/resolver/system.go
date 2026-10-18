// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package resolver asks the system DNS resolver, the servers listed in
// /etc/resolv.conf, for the answers dnscli uses as freshness hints, asks a
// Zone's own nameservers directly for its SOA serial (SOAAt) and its
// key-signing keys (DNSKEYAt), and asks the parent zone's nameservers
// directly for its DS records (DSAt).
//
// The queries through the system resolver are verified by hand, not by the
// automated suite; reading resolv.conf has tests, and SOAAt, DNSKEYAt and
// DSAt are tested against an in-process nameserver.
package resolver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	mdns "github.com/miekg/dns"
)

// ResolvConf is where the system resolver is configured.
const ResolvConf = "/etc/resolv.conf"

// System queries the nameservers from a resolv.conf file. The standard
// library's net.Resolver cannot look up SOA records, so it speaks DNS itself.
// The zero value reads ResolvConf on first use, and reads it again when
// the file changes, as when the Mac joins another network.
type System struct {
	// Path is the resolv.conf file to read; ResolvConf when empty.
	Path string

	// port, when set, is the port every query goes to instead of 53, for
	// the system resolver and for the nameservers found by name alike;
	// tests use it for an in-process nameserver.
	port string

	mu     sync.Mutex
	read   bool      // config and err hold a read of the file
	stamp  fileStamp // the file as it was when read
	readAt time.Time
	config *mdns.ClientConfig
	err    error
}

// errorRetry is how soon a failed read is tried again although the file
// looks unchanged, so a transient failure does not stick.
const errorRetry = 5 * time.Second

// fileStamp identifies one version of a file without reading it.
type fileStamp struct {
	exists  bool
	modTime time.Time
	size    int64
}

func stampOf(path string) fileStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}

	return fileStamp{exists: true, modTime: fi.ModTime(), size: fi.Size()}
}

// SOA returns the SOA serial the system resolver gives for zone.
func (s *System) SOA(ctx context.Context, zone string) (uint32, error) {
	q := new(mdns.Msg)
	q.SetQuestion(mdns.Fqdn(zone), mdns.TypeSOA)

	r, err := s.exchange(ctx, q)
	if err != nil {
		return 0, err
	}

	if serial, ok := soaSerialOf(r, zone); ok {
		return serial, nil
	}

	return 0, fmt.Errorf("no SOA record for %s", zone)
}

// soaSerialOf returns the serial of zone's SOA record in r's answer.
func soaSerialOf(r *mdns.Msg, zone string) (uint32, bool) {
	for _, rr := range r.Answer {
		if soa, ok := rr.(*mdns.SOA); ok && strings.EqualFold(soa.Hdr.Name, mdns.Fqdn(zone)) {
			return soa.Serial, true
		}
	}

	return 0, false
}

// NS returns the nameservers the system resolver gives for zone.
//
// A recursive resolver answers an NS query from the zone's own apex NS
// records, served by the nameservers the parent delegates to, not from
// the parent's delegation NS set. The two are usually the same set. Asking
// the parent's servers directly would bypass the system resolver, which a
// network may block, so dnscli takes the apex answer as the Delegation.
func (s *System) NS(ctx context.Context, zone string) ([]string, error) {
	q := new(mdns.Msg)
	q.SetQuestion(mdns.Fqdn(zone), mdns.TypeNS)

	r, err := s.exchange(ctx, q)
	if err != nil {
		return nil, err
	}

	var ns []string

	for _, rr := range r.Answer {
		if n, ok := rr.(*mdns.NS); ok && strings.EqualFold(n.Hdr.Name, q.Question[0].Name) {
			ns = append(ns, n.Ns)
		}
	}

	if len(ns) == 0 {
		return nil, fmt.Errorf("no NS records for %s", zone)
	}

	return ns, nil
}

// exchange sends q to each configured nameserver in turn, retrying over TCP
// when a UDP answer is truncated, and returns the first conclusive answer.
func (s *System) exchange(ctx context.Context, q *mdns.Msg) (*mdns.Msg, error) {
	config, err := s.load()
	if err != nil {
		return nil, err
	}

	q.SetEdns0(mdns.DefaultMsgSize, false)

	var errs []error

	for _, server := range config.Servers {
		r, err := exchangeAt(ctx, net.JoinHostPort(server, s.queryPort(config.Port)), q)

		switch {
		case err != nil:
			errs = append(errs, err)
		case r.Rcode == mdns.RcodeSuccess:
			return r, nil
		case r.Rcode == mdns.RcodeNameError:
			return nil, fmt.Errorf("%s does not exist", strings.TrimSuffix(q.Question[0].Name, "."))
		default:
			errs = append(errs, fmt.Errorf("%s answered %s", server, mdns.RcodeToString[r.Rcode]))
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	if len(errs) == 0 {
		return nil, errors.New("no nameservers in " + s.path())
	}

	return nil, errors.Join(errs...)
}

// exchangeAt sends q to one nameserver address, retrying over TCP when
// the UDP answer is truncated.
func exchangeAt(ctx context.Context, addr string, q *mdns.Msg) (*mdns.Msg, error) {
	r, err := exchangeWith(ctx, "udp", addr, q)
	if err == nil && r.Truncated {
		r, err = exchangeWith(ctx, "tcp", addr, q)
	}

	return r, err
}

func exchangeWith(ctx context.Context, network, addr string, q *mdns.Msg) (*mdns.Msg, error) {
	c := &mdns.Client{Net: network}

	r, _, err := c.ExchangeContext(ctx, q, addr)

	return r, err
}

// load returns the resolver configuration. It reads the file only when it
// has changed since the last read (a stat per query), or, after a failed
// read, once errorRetry has passed.
func (s *System) load() (*mdns.ClientConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stamp := stampOf(s.path())
	if s.read && stamp == s.stamp && (s.err == nil || time.Since(s.readAt) < errorRetry) {
		return s.config, s.err
	}

	s.read, s.stamp, s.readAt = true, stamp, time.Now()

	s.config, s.err = mdns.ClientConfigFromFile(s.path())
	if s.err != nil {
		s.config, s.err = nil, fmt.Errorf("read the system resolver configuration: %w", s.err)
	}

	return s.config, s.err
}

// queryPort is the port queries go to: the System's override when set,
// else defaultPort.
func (s *System) queryPort(defaultPort string) string {
	if s.port != "" {
		return s.port
	}

	return defaultPort
}

func (s *System) path() string {
	if s.Path == "" {
		return ResolvConf
	}

	return s.Path
}
