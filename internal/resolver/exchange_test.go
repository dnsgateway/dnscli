package resolver

import (
	"context"
	"net"
	"testing"

	mdns "github.com/miekg/dns"
)

func soaRR(name string, serial uint32) *mdns.SOA {
	return &mdns.SOA{
		Hdr:    mdns.RR_Header{Name: name, Rrtype: mdns.TypeSOA, Class: mdns.ClassINET, Ttl: 300},
		Ns:     "ns1." + name,
		Mbox:   "hostmaster." + name,
		Serial: serial,
	}
}

func TestSOASerialOfTakesTheZonesOwnSOA(t *testing.T) {
	r := new(mdns.Msg)
	r.Answer = []mdns.RR{soaRR("other.example.", 7), soaRR("Work.Example.", 2026100799)}

	if serial, ok := soaSerialOf(r, "work.example"); !ok || serial != 2026100799 {
		t.Fatalf("soaSerialOf = %d, %v; want 2026100799, true", serial, ok)
	}

	if serial, ok := soaSerialOf(r, "missing.example"); ok {
		t.Fatalf("soaSerialOf for a zone without an SOA = %d, true; want false", serial)
	}
}

// TestExchangeAtRetriesATruncatedAnswerOverTCP serves a truncated answer
// over UDP and the full one over TCP on the same port.
func TestExchangeAtRetriesATruncatedAnswerOverTCP(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	pc, err := net.ListenPacket("udp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	handler := mdns.HandlerFunc(func(w mdns.ResponseWriter, q *mdns.Msg) {
		r := new(mdns.Msg)
		r.SetReply(q)

		if w.LocalAddr().Network() == "udp" {
			r.Truncated = true
		} else {
			r.Answer = append(r.Answer, soaRR(q.Question[0].Name, 42))
		}

		_ = w.WriteMsg(r)
	})

	for _, srv := range []*mdns.Server{{PacketConn: pc, Handler: handler}, {Listener: l, Handler: handler}} {
		started := make(chan struct{})
		srv.NotifyStartedFunc = func() { close(started) }

		go func() { _ = srv.ActivateAndServe() }()

		<-started
		t.Cleanup(func() { _ = srv.Shutdown() })
	}

	q := new(mdns.Msg)
	q.SetQuestion("work.example.", mdns.TypeSOA)

	r, err := exchangeAt(context.Background(), l.Addr().String(), q)
	if err != nil {
		t.Fatalf("exchangeAt: %v", err)
	}

	if serial, ok := soaSerialOf(r, "work.example"); !ok || serial != 42 {
		t.Fatalf("serial = %d, %v; want 42 from the TCP answer", serial, ok)
	}
}
