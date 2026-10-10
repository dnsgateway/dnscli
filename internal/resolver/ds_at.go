package resolver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	mdns "github.com/miekg/dns"
)

// DSAt returns the Delegation Signer (DS) records the parent zone serves
// for zone, in presentation form after the type ("2371 13 2 1F98…"),
// sorted, and the TTL of their set. A parent that serves none gives no
// records and a zero TTL, without error.
//
// It finds the parent zone, the nearest zone above zone, by asking the
// system resolver for the nameservers of each name above zone in turn,
// then asks those nameservers in turn, directly and without recursion,
// and takes the first authoritative answer. The nameservers are reached
// at the addresses the resolver's answer gives for them, else at those
// the system's usual lookup gives.
func (s *System) DSAt(ctx context.Context, zone string) ([]string, time.Duration, error) {
	parent, servers, err := s.parentOf(ctx, zone)
	if err != nil {
		return nil, 0, err
	}

	q := new(mdns.Msg)
	q.SetQuestion(mdns.Fqdn(zone), mdns.TypeDS)
	q.RecursionDesired = false
	q.SetEdns0(mdns.DefaultMsgSize, false)

	var errs []error

	for _, ns := range servers {
		addrs := ns.addrs
		if len(addrs) == 0 {
			addrs, err = serverAddrs(ctx, ns.host, s.portOr("53"))
			if err != nil {
				errs = append(errs, err)

				continue
			}
		}

		for _, addr := range addrs {
			ds, ttl, err := dsFrom(ctx, addr, q, parent)
			if err == nil {
				return ds, ttl, nil
			}

			errs = append(errs, fmt.Errorf("%s: %w", strings.TrimSuffix(ns.host, "."), err))

			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
		}
	}

	return nil, 0, errors.Join(errs...)
}

// nameserver is one nameserver of a zone: its host name, and the
// addresses, with ports, the resolver gave for it, if any.
type nameserver struct {
	host  string
	addrs []string
}

// parentOf finds the zone above zone, and its nameservers, through the
// system resolver: the nearest name above zone that has NS records.
func (s *System) parentOf(ctx context.Context, zone string) (string, []nameserver, error) {
	name := mdns.Fqdn(zone)

	for name != "." {
		i, _ := mdns.NextLabel(name, 0)
		name = name[i:]

		if name == "" {
			name = "."
		}

		q := new(mdns.Msg)
		q.SetQuestion(name, mdns.TypeNS)

		r, err := s.exchange(ctx, q)
		if err != nil {
			return "", nil, fmt.Errorf("find the parent zone of %s: %w", strings.TrimSuffix(zone, "."), err)
		}

		if servers := nameserversOf(r, name, s.portOr("53")); len(servers) > 0 {
			return name, servers, nil
		}
	}

	return "", nil, fmt.Errorf("find the parent zone of %s: the resolver gave no nameservers above it", strings.TrimSuffix(zone, "."))
}

// nameserversOf returns zone's nameservers in an answer to an NS query,
// with the addresses its additional section gives for them on port.
func nameserversOf(r *mdns.Msg, zone, port string) []nameserver {
	var servers []nameserver

	for _, rr := range r.Answer {
		n, ok := rr.(*mdns.NS)
		if !ok || !strings.EqualFold(n.Hdr.Name, zone) {
			continue
		}

		ns := nameserver{host: n.Ns}

		for _, extra := range r.Extra {
			if !strings.EqualFold(extra.Header().Name, n.Ns) {
				continue
			}

			switch a := extra.(type) {
			case *mdns.A:
				ns.addrs = append(ns.addrs, net.JoinHostPort(a.A.String(), port))
			case *mdns.AAAA:
				ns.addrs = append(ns.addrs, net.JoinHostPort(a.AAAA.String(), port))
			}
		}

		servers = append(servers, ns)
	}

	return servers
}

// dsFrom sends the DS query q to one nameserver address of the parent
// zone and returns the DS records of its authoritative answer, and their
// TTL.
func dsFrom(ctx context.Context, addr string, q *mdns.Msg, parent string) ([]string, time.Duration, error) {
	r, err := exchangeAt(ctx, addr, q)

	switch {
	case err != nil:
		return nil, 0, err
	case r.Rcode != mdns.RcodeSuccess:
		return nil, 0, fmt.Errorf("answered %s", mdns.RcodeToString[r.Rcode])
	case !r.Authoritative:
		return nil, 0, fmt.Errorf("is not authoritative for %s", parent)
	}

	var (
		ds  []string
		ttl uint32
	)

	for _, rr := range r.Answer {
		d, ok := rr.(*mdns.DS)
		if !ok || !strings.EqualFold(d.Hdr.Name, q.Question[0].Name) {
			continue
		}

		if len(ds) == 0 || d.Hdr.Ttl < ttl {
			ttl = d.Hdr.Ttl
		}

		ds = append(ds, fmt.Sprintf("%d %d %d %s", d.KeyTag, d.Algorithm, d.DigestType, strings.ToUpper(d.Digest)))
	}

	slices.Sort(ds)

	return ds, time.Duration(ttl) * time.Second, nil
}
