package resolver

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
)

// parentServer is an in-process nameserver on one local port, over UDP and
// TCP, that plays both the system resolver and the parent zone's
// authoritative server: it answers recursive NS queries for the zone
// names in ns, giving glue for each nameserver, and non-recursive DS
// queries for the zone names in ds, authoritatively.
type parentServer struct {
	port string

	// ns are the zones the resolver knows the nameservers of, with the
	// address each nameserver's glue gives.
	ns map[string]map[string]string
	// ds are the DS records the parent serves, by child zone name.
	ds map[string][]*mdns.DS
	// truncateUDP truncates every DS answer sent over UDP.
	truncateUDP bool

	mu      sync.Mutex
	dsAsked []dsQuery
}

// dsQuery is one DS query the parent server got.
type dsQuery struct {
	name    string
	network string
	rd      bool
}

func dsRR(zone string, ttl uint32, tag uint16, digest string) *mdns.DS {
	return &mdns.DS{
		Hdr:        mdns.RR_Header{Name: mdns.Fqdn(zone), Rrtype: mdns.TypeDS, Class: mdns.ClassINET, Ttl: ttl},
		KeyTag:     tag,
		Algorithm:  mdns.ECDSAP256SHA256,
		DigestType: mdns.SHA256,
		Digest:     digest,
	}
}

// serveParent starts p on a local port and returns a System whose
// resolver and nameserver queries all go to it.
func serveParent(t *testing.T, p *parentServer) *System {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	pc, err := net.ListenPacket("udp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	_, p.port, _ = net.SplitHostPort(l.Addr().String())

	for _, srv := range []*mdns.Server{{PacketConn: pc, Handler: p}, {Listener: l, Handler: p}} {
		started := make(chan struct{})
		srv.NotifyStartedFunc = func() { close(started) }

		go func() { _ = srv.ActivateAndServe() }()

		<-started
		t.Cleanup(func() { _ = srv.Shutdown() })
	}

	conf := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(conf, []byte("nameserver 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return &System{Path: conf, port: p.port}
}

func (p *parentServer) ServeDNS(w mdns.ResponseWriter, q *mdns.Msg) {
	r := new(mdns.Msg)
	r.SetReply(q)

	name := q.Question[0].Name

	switch q.Question[0].Qtype {
	case mdns.TypeNS:
		servers, ok := p.ns[name]
		if !ok {
			break // no zone cut here: an empty answer
		}

		for host, addr := range servers {
			r.Answer = append(r.Answer, &mdns.NS{Hdr: mdns.RR_Header{Name: name, Rrtype: mdns.TypeNS, Class: mdns.ClassINET, Ttl: 86400}, Ns: host})
			r.Extra = append(r.Extra, &mdns.A{Hdr: mdns.RR_Header{Name: host, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 86400}, A: net.ParseIP(addr)})
		}
	case mdns.TypeDS:
		p.mu.Lock()
		p.dsAsked = append(p.dsAsked, dsQuery{name: name, network: w.LocalAddr().Network(), rd: q.RecursionDesired})
		p.mu.Unlock()

		r.Authoritative = true

		if p.truncateUDP && w.LocalAddr().Network() == "udp" {
			r.Truncated = true

			break
		}

		for _, ds := range p.ds[name] {
			r.Answer = append(r.Answer, ds)
		}
	default:
		r.Rcode = mdns.RcodeRefused
	}

	_ = w.WriteMsg(r)
}

func (p *parentServer) dsQueries() []dsQuery {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.dsAsked)
}

func TestDSAtAsksTheParentsNameserversDirectlyForTheDSSetAndItsTTL(t *testing.T) {
	p := &parentServer{
		ns: map[string]map[string]string{"example.": {"a.nic.example.": "127.0.0.1"}},
		ds: map[string][]*mdns.DS{"work.example.": {
			dsRR("work.example", 3600, 60485, "d4b7d520e7bb5f0f67674a0cceb1e3e0614b93c4f9e99b8383f6a1e4469da50a"),
			dsRR("work.example", 3600, 2371, "1f987cc6583e92df0890718c42"),
		}},
	}
	s := serveParent(t, p)

	ds, ttl, err := s.DSAt(context.Background(), "work.example")
	if err != nil {
		t.Fatalf("DSAt: %v", err)
	}

	want := []string{
		"2371 13 2 1F987CC6583E92DF0890718C42",
		"60485 13 2 D4B7D520E7BB5F0F67674A0CCEB1E3E0614B93C4F9E99B8383F6A1E4469DA50A",
	}
	if !slices.Equal(ds, want) || ttl != time.Hour {
		t.Fatalf("DSAt = %q, %v; want %q, 1h", ds, ttl, want)
	}

	if q := p.dsQueries(); len(q) != 1 || q[0].name != "work.example." || q[0].rd {
		t.Fatalf("DS queries = %+v, want one for work.example. without recursion", q)
	}
}

func TestDSAtFindsTheParentZoneAboveANameThatIsNotAZoneCut(t *testing.T) {
	p := &parentServer{
		ns: map[string]map[string]string{"example.": {"a.nic.example.": "127.0.0.1"}},
		ds: map[string][]*mdns.DS{"shop.co.example.": {dsRR("shop.co.example", 300, 2371, "1f987cc6583e92df0890718c42")}},
	}
	s := serveParent(t, p)

	ds, ttl, err := s.DSAt(context.Background(), "shop.co.example")
	if err != nil {
		t.Fatalf("DSAt: %v", err)
	}

	if len(ds) != 1 || ttl != 5*time.Minute {
		t.Fatalf("DSAt = %q, %v; want the one DS, 5m", ds, ttl)
	}
}

func TestDSAtReportsNoDSWhenTheParentServesNone(t *testing.T) {
	p := &parentServer{ns: map[string]map[string]string{"example.": {"a.nic.example.": "127.0.0.1"}}}
	s := serveParent(t, p)

	ds, ttl, err := s.DSAt(context.Background(), "work.example")
	if err != nil {
		t.Fatalf("DSAt: %v", err)
	}

	if len(ds) != 0 || ttl != 0 {
		t.Fatalf("DSAt = %q, %v; want no DS", ds, ttl)
	}
}

func TestDSAtFailsWhenTheParentIsUnreachable(t *testing.T) {
	// The parent's only nameserver is at an address nothing answers on.
	p := &parentServer{ns: map[string]map[string]string{"example.": {"a.nic.example.": "192.0.2.1"}}}
	s := serveParent(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if ds, _, err := s.DSAt(ctx, "work.example"); err == nil {
		t.Fatalf("DSAt = %q, want an error", ds)
	}
}

func TestDSAtRetriesATruncatedAnswerOverTCP(t *testing.T) {
	p := &parentServer{
		ns:          map[string]map[string]string{"example.": {"a.nic.example.": "127.0.0.1"}},
		ds:          map[string][]*mdns.DS{"work.example.": {dsRR("work.example", 3600, 2371, "1f987cc6583e92df0890718c42")}},
		truncateUDP: true,
	}
	s := serveParent(t, p)

	ds, _, err := s.DSAt(context.Background(), "work.example")
	if err != nil {
		t.Fatalf("DSAt: %v", err)
	}

	if len(ds) != 1 {
		t.Fatalf("DSAt = %q, want the DS from the TCP answer", ds)
	}

	if q := p.dsQueries(); len(q) != 2 || q[0].network != "udp" || q[1].network != "tcp" {
		t.Fatalf("DS queries = %+v, want one over UDP then one over TCP", q)
	}
}
