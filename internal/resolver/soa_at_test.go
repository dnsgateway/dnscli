// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package resolver

import (
	"context"
	"net"
	"sync"
	"testing"

	mdns "github.com/miekg/dns"
)

// authServer is an in-process authoritative nameserver for one zone.
type authServer struct {
	addr string

	mu sync.Mutex
	rd []bool // RecursionDesired of each query, in order
}

// serveSOA starts a nameserver on a local UDP port that answers SOA
// queries for zone with serial, flagged authoritative when auth is set,
// and refuses everything else.
func serveSOA(t *testing.T, zone string, serial uint32, auth bool) *authServer {
	t.Helper()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	a := &authServer{addr: pc.LocalAddr().String()}

	handler := mdns.HandlerFunc(func(w mdns.ResponseWriter, q *mdns.Msg) {
		a.mu.Lock()
		a.rd = append(a.rd, q.RecursionDesired)
		a.mu.Unlock()

		r := new(mdns.Msg)
		r.SetReply(q)
		r.Authoritative = auth

		if q.Question[0].Qtype != mdns.TypeSOA || q.Question[0].Name != mdns.Fqdn(zone) {
			r.Rcode = mdns.RcodeRefused
		} else {
			r.Answer = append(r.Answer, &mdns.SOA{
				Hdr:    mdns.RR_Header{Name: mdns.Fqdn(zone), Rrtype: mdns.TypeSOA, Class: mdns.ClassINET, Ttl: 300},
				Ns:     "ns1." + mdns.Fqdn(zone),
				Mbox:   "hostmaster." + mdns.Fqdn(zone),
				Serial: serial,
			})
		}

		_ = w.WriteMsg(r)
	})

	started := make(chan struct{})
	srv := &mdns.Server{PacketConn: pc, Handler: handler, NotifyStartedFunc: func() { close(started) }}

	go func() { _ = srv.ActivateAndServe() }()

	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })

	return a
}

func (a *authServer) recursionDesired() []bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([]bool(nil), a.rd...)
}

func TestSOAAtAsksTheZonesNameserverDirectlyWithoutRecursion(t *testing.T) {
	srv := serveSOA(t, "work.example", 2026100799, true)

	// The system resolver is not consulted: its configuration is missing.
	s := &System{Path: "/nonexistent/resolv.conf"}

	serial, err := s.SOAAt(context.Background(), "work.example", []string{srv.addr})
	if err != nil {
		t.Fatalf("SOAAt: %v", err)
	}

	if serial != 2026100799 {
		t.Fatalf("serial = %d, want 2026100799", serial)
	}

	if rd := srv.recursionDesired(); len(rd) != 1 || rd[0] {
		t.Fatalf("queries asked for recursion: %v, want one query without", rd)
	}
}

func TestSOAAtTriesTheNextNameserverWhenOneFails(t *testing.T) {
	notAuth := serveSOA(t, "work.example", 1, false)
	srv := serveSOA(t, "work.example", 2026100799, true)

	s := &System{}

	serial, err := s.SOAAt(context.Background(), "work.example", []string{notAuth.addr, srv.addr})
	if err != nil {
		t.Fatalf("SOAAt: %v", err)
	}

	if serial != 2026100799 {
		t.Fatalf("serial = %d, want 2026100799 from the authoritative server", serial)
	}
}

func TestSOAAtFailsWithoutAnAuthoritativeAnswer(t *testing.T) {
	notAuth := serveSOA(t, "work.example", 1, false)
	other := serveSOA(t, "other.example", 7, true)

	s := &System{}

	for _, servers := range [][]string{nil, {notAuth.addr}, {other.addr}} {
		if serial, err := s.SOAAt(context.Background(), "work.example", servers); err == nil {
			t.Fatalf("SOAAt(%v) = %d, want an error", servers, serial)
		}
	}
}
