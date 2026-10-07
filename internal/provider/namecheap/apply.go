package namecheap

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// setHostsResult is a setHosts CommandResponse.
type setHostsResult struct {
	Result struct {
		IsSuccess bool `xml:"IsSuccess,attr"`
	} `xml:"DomainDNSSetHostsResult"`
}

// sendHost is one host of a setHosts request.
type sendHost struct {
	Name, Type, Address string
	// MXPref is sent when not empty.
	MXPref string
	TTL    uint32
}

// Apply implements provider.Provider. setHosts replaces a domain's whole
// host list, so Apply reads the list and the EmailType with getHosts, makes
// the Changeset's changes to it, and sends the result in one call. Every
// change shares that call's result.
func (p *Provider) Apply(ctx context.Context, cs dns.Changeset) (provider.ApplyResult, error) {
	res := provider.ApplyResult{Results: make([]provider.ChangeResult, len(cs.Changes))}
	for i, c := range cs.Changes {
		res.Results[i] = provider.ChangeResult{Change: c}
	}

	if len(cs.Changes) == 0 {
		return res, nil
	}

	if err := writable(cs); err != nil {
		return failAll(res, err), nil
	}

	current, err := p.getHosts(ctx, cs.Zone)
	if err != nil {
		return failAll(res, err), nil
	}

	// slots are the hosts as read, each replaced or emptied by the change
	// that matches it, then the hosts added.
	type slot struct {
		read    *wireHost
		send    *sendHost
		matched bool
	}

	slots := make([]slot, 0, len(current.Result.Hosts)+len(cs.Changes))
	for _, h := range current.Result.Hosts {
		send := asRead(h)
		slots = append(slots, slot{read: &h, send: &send})
	}

	for _, c := range cs.Changes {
		var send *sendHost

		if c.Kind != dns.ChangeDelete {
			h, err := toHost(cs.Zone.Name, c.After)
			if err != nil {
				return failAll(res, err), nil
			}

			send = &h
		}

		if c.Kind == dns.ChangeAdd {
			slots = append(slots, slot{send: send})

			continue
		}

		i := slices.IndexFunc(slots, func(s slot) bool {
			return s.read != nil && !s.matched && matches(cs.Zone.Name, *s.read, c.Before)
		})
		if i < 0 {
			return failAll(res, fmt.Errorf("namecheap: conflict, nothing was applied: %s %s %s changed at Namecheap: %w",
				c.Before.Type, c.Before.Name, c.Before.Value, provider.ErrNotFound)), nil
		}

		slots[i].matched, slots[i].send = true, send
	}

	hosts := make([]sendHost, 0, len(slots))

	for _, s := range slots {
		if s.send != nil {
			hosts = append(hosts, *s.send)
		}
	}

	if err := p.setHosts(ctx, cs.Zone, current.Result.EmailType, hosts); err != nil {
		return failAll(res, err), nil
	}

	for i, c := range cs.Changes {
		if c.Kind != dns.ChangeDelete {
			// setHosts gives every host a new HostId, which the next
			// Refresh reads.
			res.Results[i].Record = c.After
			res.Results[i].Record.ProviderID = ""
		}
	}

	return res, nil
}

// matches reports whether a host as read is the Record a change was built
// from: the host with its HostId, or any host when it has none, that still
// has its type, name and value. A host edited at Namecheap since is not it.
func matches(zone string, h wireHost, before dns.Record) bool {
	if before.ProviderID != "" && h.HostID != before.ProviderID {
		return false
	}

	r := toRecord(zone, h)

	return strings.EqualFold(r.Type, before.Type) && strings.EqualFold(r.Name, before.Name) && r.Value == before.Value
}

// writeTypes are the Record types Apply adds and modifies. Namecheap-only
// types (URL, URL301, FRAME, ALIAS, MXE) are kept, not written; SRV waits
// for the manual pass to confirm Namecheap's form shape for it.
var writeTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "CAA"}

// writable is an error naming each Record a Changeset adds or modifies to
// a type Apply does not write, or to NS at the apex; nil if there is none.
func writable(cs dns.Changeset) error {
	var bad []string

	for _, c := range cs.Changes {
		r := c.After
		if c.Kind == dns.ChangeDelete {
			continue
		}

		if !slices.Contains(writeTypes, r.Type) || (r.Type == "NS" && relativeName(cs.Zone.Name, r.Name) == "@") {
			bad = append(bad, r.Type+" "+r.Name)
		}
	}

	if bad == nil {
		return nil
	}

	return fmt.Errorf("namecheap: dnscli writes only A, AAAA, CNAME, MX, TXT, CAA and NS below the apex, not %s; nothing was applied",
		strings.Join(bad, ", "))
}

// failAll fails every change with err.
func failAll(res provider.ApplyResult, err error) provider.ApplyResult {
	for i := range res.Results {
		res.Results[i].Err = err
	}

	return res
}

// asRead is a host getHosts answered, to send back exactly as read.
func asRead(h wireHost) sendHost {
	return sendHost{
		Name: h.Name, Type: h.Type, Address: h.Address,
		MXPref: strconv.FormatUint(uint64(h.MXPref), 10), TTL: h.TTL,
	}
}

// toHost undoes what toRecord does to a Record of a Zone: its name becomes
// relative ("@" for the apex), the MX priority leaves the Value for MXPref,
// and host name targets end in a dot.
func toHost(zone string, r dns.Record) (sendHost, error) {
	h := sendHost{Name: relativeName(zone, r.Name), Type: r.Type, Address: r.Value, TTL: r.TTL}

	switch r.Type {
	case "CNAME", "NS":
		h.Address = fqdn(r.Value)
	case "MX":
		pref, host, ok := strings.Cut(r.Value, " ")
		if _, err := strconv.ParseUint(pref, 10, 16); !ok || err != nil {
			return sendHost{}, fmt.Errorf("namecheap: MX value %q is not \"priority host\"", r.Value)
		}

		h.MXPref, h.Address = pref, fqdn(strings.TrimSpace(host))
	}

	return h, nil
}

// fqdn is a host name target with its trailing dot; the root stays ".".
func fqdn(name string) string {
	if strings.HasSuffix(name, ".") {
		return name
	}

	return name + "."
}

// relativeName is an absolute Record name relative to its Zone, "@" for
// the apex, keeping the name's own case.
func relativeName(zone, name string) string {
	name = strings.TrimSuffix(name, ".")
	lower, z := strings.ToLower(name), strings.ToLower(strings.TrimSuffix(zone, "."))

	switch {
	case lower == z:
		return "@"
	case strings.HasSuffix(lower, "."+z):
		return name[:len(name)-len(z)-1]
	}

	return name
}

// setHosts replaces a Zone's hosts and its EmailType.
func (p *Provider) setHosts(ctx context.Context, zone dns.Zone, emailType string, hosts []sendHost) error {
	sld, tld := splitDomain(zone.ProviderID)

	form := url.Values{"SLD": {sld}, "TLD": {tld}}
	if emailType != "" {
		form.Set("EmailType", emailType)
	}

	for i, h := range hosts {
		n := strconv.Itoa(i + 1)
		form.Set("HostName"+n, h.Name)
		form.Set("RecordType"+n, h.Type)
		form.Set("Address"+n, h.Address)
		form.Set("TTL"+n, strconv.FormatUint(uint64(h.TTL), 10))

		if h.MXPref != "" {
			form.Set("MXPref"+n, h.MXPref)
		}
	}

	res, err := call[setHostsResult](ctx, p, "namecheap.domains.dns.setHosts", form)
	if err != nil {
		return err
	}

	if !res.Result.IsSuccess {
		return errors.New("namecheap: setHosts answered without success; the Zone may be unchanged, Refresh it")
	}

	return nil
}
