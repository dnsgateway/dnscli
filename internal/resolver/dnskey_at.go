package resolver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	mdns "github.com/miekg/dns"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// DNSKEYAt returns the key-signing keys, the DNSKEY records with flag 257,
// that zone's authoritative nameservers serve, each with its SHA-256 DS
// record, sorted by DNSKEY. It asks every one of servers, directly and
// without recursion, and returns the keys only when they all serve the
// same ones, in a DNSKEY RRset that validates under its own RRSIG.
// Nameservers that serve no DNSKEY give no keys, without error.
//
// A server that serves other keys than the first fails it with an error
// that wraps dns.ErrNameserversDisagree and names that server; one whose
// keys do not validate, or that does not answer, fails it naming it too.
// A server is reached as SOAAt reaches it.
func (s *System) DNSKEYAt(ctx context.Context, zone string, servers []string) ([]dns.SigningKey, error) {
	if len(servers) == 0 {
		return nil, fmt.Errorf("no nameservers to ask for the DNSKEY of %s", zone)
	}

	q := new(mdns.Msg)
	q.SetQuestion(mdns.Fqdn(zone), mdns.TypeDNSKEY)
	q.RecursionDesired = false
	q.SetEdns0(mdns.DefaultMsgSize, true)

	var (
		keys  []dns.SigningKey
		first string
	)

	for i, server := range servers {
		got, err := s.keysAt(ctx, server, q)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", server, err)
		}

		if i == 0 {
			keys, first = got, server

			continue
		}

		if !slices.EqualFunc(keys, got, func(a, b dns.SigningKey) bool { return a.DNSKEY == b.DNSKEY }) {
			return nil, fmt.Errorf("%s: %w: it serves %s, %s serves %s",
				server, dns.ErrNameserversDisagree, keyTags(got), first, keyTags(keys))
		}
	}

	return keys, nil
}

// keysAt asks one nameserver, at each of its addresses in turn, for the
// key-signing keys of q's zone, and returns those of the first answer.
func (s *System) keysAt(ctx context.Context, server string, q *mdns.Msg) ([]dns.SigningKey, error) {
	addrs, err := serverAddrs(ctx, server, s.queryPort("53"))
	if err != nil {
		return nil, err
	}

	var errs []error

	for _, addr := range addrs {
		keys, err := keysFrom(ctx, addr, q)
		if err == nil {
			return keys, nil
		}

		errs = append(errs, err)

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	return nil, errors.Join(errs...)
}

// keysFrom sends the DNSKEY query q to one nameserver address and returns
// the key-signing keys of its authoritative answer, once their RRset
// validates under one of its own RRSIGs.
func keysFrom(ctx context.Context, addr string, q *mdns.Msg) ([]dns.SigningKey, error) {
	r, err := exchangeAt(ctx, addr, q)

	switch {
	case err != nil:
		return nil, err
	case r.Rcode != mdns.RcodeSuccess:
		return nil, fmt.Errorf("answered %s", mdns.RcodeToString[r.Rcode])
	case !r.Authoritative:
		return nil, errors.New("is not authoritative for the zone")
	}

	name := q.Question[0].Name

	var (
		rrset []mdns.RR
		sigs  []*mdns.RRSIG
	)

	for _, rr := range r.Answer {
		if !strings.EqualFold(rr.Header().Name, name) {
			continue
		}

		switch rr := rr.(type) {
		case *mdns.DNSKEY:
			rrset = append(rrset, rr)
		case *mdns.RRSIG:
			if rr.TypeCovered == mdns.TypeDNSKEY {
				sigs = append(sigs, rr)
			}
		}
	}

	if len(rrset) == 0 {
		return nil, nil
	}

	if !selfSigned(name, rrset, sigs, time.Now()) {
		return nil, errors.New("its DNSKEY records do not validate under their own RRSIG")
	}

	var keys []dns.SigningKey

	for _, rr := range rrset {
		k := rr.(*mdns.DNSKEY)
		if k.Flags != mdns.ZONE|mdns.SEP {
			continue // a zone-signing key, or a revoked key
		}

		key, err := dns.SigningKeyOf(k)
		if err != nil {
			return nil, err
		}

		keys = append(keys, key)
	}

	slices.SortFunc(keys, func(a, b dns.SigningKey) int { return strings.Compare(a.DNSKEY, b.DNSKEY) })

	return keys, nil
}

// selfSigned reports whether zone's DNSKEY RRset validates, at now, under
// one of sigs made by zone itself, with a key in the set.
func selfSigned(zone string, rrset []mdns.RR, sigs []*mdns.RRSIG, now time.Time) bool {
	for _, sig := range sigs {
		if !sig.ValidityPeriod(now) || !strings.EqualFold(mdns.Fqdn(sig.SignerName), mdns.Fqdn(zone)) {
			continue
		}

		for _, rr := range rrset {
			k := rr.(*mdns.DNSKEY)
			if k.KeyTag() == sig.KeyTag && k.Algorithm == sig.Algorithm && sig.Verify(k, rrset) == nil {
				return true
			}
		}
	}

	return false
}

// keyTags names a set of key-signing keys by their key tags, for an error.
func keyTags(keys []dns.SigningKey) string {
	if len(keys) == 0 {
		return "no key-signing key"
	}

	tags := make([]string, len(keys))
	for i, k := range keys {
		tags[i] = strconv.Itoa(int(k.KeyTag))
	}

	return "the keys with tags " + strings.Join(tags, ", ")
}
