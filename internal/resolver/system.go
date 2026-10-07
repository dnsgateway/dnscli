// Package resolver asks the system DNS resolver, the servers listed in
// /etc/resolv.conf, for the answers dnscli uses as freshness hints.
//
// It is verified by hand, not by the automated suite.
package resolver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	mdns "github.com/miekg/dns"
)

// ResolvConf is where the system resolver is configured.
const ResolvConf = "/etc/resolv.conf"

// System queries the nameservers from a resolv.conf file. The standard
// library's net.Resolver cannot look up SOA records, so it speaks DNS itself.
// The zero value reads ResolvConf on first use.
type System struct {
	// Path is the resolv.conf file to read; ResolvConf when empty.
	Path string

	once   sync.Once
	config *mdns.ClientConfig
	err    error
}

// SOA returns the SOA serial the system resolver gives for zone.
func (s *System) SOA(ctx context.Context, zone string) (uint32, error) {
	q := new(mdns.Msg)
	q.SetQuestion(mdns.Fqdn(zone), mdns.TypeSOA)

	r, err := s.exchange(ctx, q)
	if err != nil {
		return 0, err
	}

	for _, rr := range r.Answer {
		if soa, ok := rr.(*mdns.SOA); ok && strings.EqualFold(soa.Hdr.Name, q.Question[0].Name) {
			return soa.Serial, nil
		}
	}

	return 0, fmt.Errorf("no SOA record for %s", zone)
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
		addr := net.JoinHostPort(server, config.Port)

		r, err := exchangeWith(ctx, "udp", addr, q)
		if err == nil && r.Truncated {
			r, err = exchangeWith(ctx, "tcp", addr, q)
		}

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

func exchangeWith(ctx context.Context, network, addr string, q *mdns.Msg) (*mdns.Msg, error) {
	c := &mdns.Client{Net: network}

	r, _, err := c.ExchangeContext(ctx, q, addr)

	return r, err
}

func (s *System) load() (*mdns.ClientConfig, error) {
	s.once.Do(func() {
		s.config, s.err = mdns.ClientConfigFromFile(s.path())
		if s.err != nil {
			s.err = fmt.Errorf("read the system resolver configuration: %w", s.err)
		}
	})

	return s.config, s.err
}

func (s *System) path() string {
	if s.Path == "" {
		return ResolvConf
	}

	return s.Path
}
