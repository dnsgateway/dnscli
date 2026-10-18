// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package resolver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	mdns "github.com/miekg/dns"
)

// SOAAt returns the SOA serial zone's authoritative nameservers give. It
// asks each of servers in turn, directly and without recursion, rather than
// through the system resolver, and returns the first authoritative answer.
//
// A server is a host name or an IP address, optionally with a port (53 when
// none). Host names are resolved to addresses through the system's usual
// lookup; only the SOA query itself goes straight to the nameserver.
func (s *System) SOAAt(ctx context.Context, zone string, servers []string) (uint32, error) {
	q := new(mdns.Msg)
	q.SetQuestion(mdns.Fqdn(zone), mdns.TypeSOA)
	q.RecursionDesired = false
	q.SetEdns0(mdns.DefaultMsgSize, false)

	var errs []error

	for _, server := range servers {
		addrs, err := serverAddrs(ctx, server, s.queryPort("53"))
		if err != nil {
			errs = append(errs, err)

			continue
		}

		for _, addr := range addrs {
			serial, err := soaFrom(ctx, addr, q)
			if err == nil {
				return serial, nil
			}

			errs = append(errs, fmt.Errorf("%s: %w", server, err))

			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
		}
	}

	if len(errs) == 0 {
		return 0, fmt.Errorf("no nameservers to ask for the SOA of %s", zone)
	}

	return 0, errors.Join(errs...)
}

// serverAddrs returns the addresses, with ports, to reach a nameserver at;
// on defaultPort when server names none.
func serverAddrs(ctx context.Context, server, defaultPort string) ([]string, error) {
	host, port, err := net.SplitHostPort(server)
	if err != nil {
		host, port = strings.TrimSuffix(server, "."), defaultPort
	}

	if net.ParseIP(host) != nil {
		return []string{net.JoinHostPort(host, port)}, nil
	}

	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("look up nameserver %s: %w", host, err)
	}

	addrs := make([]string, len(ips))
	for i, ip := range ips {
		addrs[i] = net.JoinHostPort(ip, port)
	}

	return addrs, nil
}

// soaFrom sends the SOA query q to one nameserver address and returns the
// serial of its authoritative answer.
func soaFrom(ctx context.Context, addr string, q *mdns.Msg) (uint32, error) {
	r, err := exchangeAt(ctx, addr, q)

	switch {
	case err != nil:
		return 0, err
	case r.Rcode != mdns.RcodeSuccess:
		return 0, fmt.Errorf("answered %s", mdns.RcodeToString[r.Rcode])
	case !r.Authoritative:
		return 0, errors.New("is not authoritative for the zone")
	}

	if serial, ok := soaSerialOf(r, q.Question[0].Name); ok {
		return serial, nil
	}

	return 0, errors.New("gave no SOA record")
}
