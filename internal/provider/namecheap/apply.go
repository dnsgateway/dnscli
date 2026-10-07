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
// the Changeset's changes to it, and sends the result in one call.
//
// The Changeset succeeds or fails as a whole. When it fails, nothing was
// applied (or, when the write's answer was lost, Namecheap may have taken
// it), so Apply returns an error of its own rather than per-change errors:
// the TUI then reports the changes as not applied and keeps them pending.
// On success, each added or modified Record is returned as Records reads
// it back, with its content ID, so it is cached without a Refresh.
func (p *Provider) Apply(ctx context.Context, cs dns.Changeset) (provider.ApplyResult, error) {
	res := provider.ApplyResult{Results: make([]provider.ChangeResult, len(cs.Changes))}
	for i, c := range cs.Changes {
		res.Results[i] = provider.ChangeResult{Change: c}
	}

	if len(cs.Changes) == 0 {
		return res, nil
	}

	if err := writable(cs); err != nil {
		return provider.ApplyResult{}, err
	}

	current, err := p.getHosts(ctx, cs.Zone)
	if err != nil {
		return provider.ApplyResult{}, fmt.Errorf("%w; nothing was applied", err)
	}

	if err := mailWritable(cs, current.Result.EmailType); err != nil {
		return provider.ApplyResult{}, err
	}

	hosts, sent, err := applyTo(cs, current.Result.Hosts)
	if err != nil {
		return provider.ApplyResult{}, err
	}

	if err := p.setHosts(ctx, cs.Zone, current.Result.EmailType, hosts); err != nil {
		return provider.ApplyResult{}, err
	}

	for i, h := range sent {
		if h != nil {
			res.Results[i].Record = h.read(cs.Zone.Name)
		}
	}

	return res, nil
}

// applyTo makes a Changeset's changes to the hosts as read: each host a
// change was built from is replaced, in its place, or left out; the hosts
// added go at the end; every other host is sent back exactly as read. sent
// are the hosts each addition or modification sends, by change.
func applyTo(cs dns.Changeset, read []wireHost) (hosts []sendHost, sent []*sendHost, err error) {
	type slot struct {
		read    *wireHost
		send    *sendHost
		matched bool
	}

	slots := make([]slot, 0, len(read)+len(cs.Changes))
	for _, h := range read {
		send := asRead(h)
		slots = append(slots, slot{read: &h, send: &send})
	}

	sent = make([]*sendHost, len(cs.Changes))

	for ci, c := range cs.Changes {
		if c.Kind != dns.ChangeDelete {
			h, err := toHost(cs.Zone.Name, c.After)
			if err != nil {
				return nil, nil, err
			}

			sent[ci] = &h
		}

		if c.Kind == dns.ChangeAdd {
			slots = append(slots, slot{send: sent[ci]})

			continue
		}

		i := slices.IndexFunc(slots, func(s slot) bool {
			return s.read != nil && !s.matched && matches(cs.Zone.Name, *s.read, c.Before)
		})
		if i < 0 {
			return nil, nil, fmt.Errorf("namecheap: conflict, nothing was applied: %s %s %s changed at Namecheap since the last Refresh (%w); Refresh the Zone and review the pending changes",
				c.Before.Type, c.Before.Name, c.Before.Value, provider.ErrNotFound)
		}

		slots[i].matched, slots[i].send = true, sent[ci]
	}

	hosts = make([]sendHost, 0, len(slots))

	for _, s := range slots {
		if s.send != nil {
			hosts = append(hosts, *s.send)
		}
	}

	return hosts, sent, nil
}

// matches reports whether a host as read is the Record a change was built
// from: the host whose content is the Record's Provider ID (withID), or,
// for a Record without one, any host with its type, name and value. A
// host edited at Namecheap since is not it.
func matches(zone string, h wireHost, before dns.Record) bool {
	r := toRecord(zone, h)
	if before.ProviderID != "" {
		return r.ProviderID == before.ProviderID
	}

	return strings.EqualFold(r.Type, before.Type) && strings.EqualFold(r.Name, before.Name) && r.Value == before.Value
}

// writeTypes are the Record types Apply adds and modifies. Namecheap-only
// types (URL, URL301, FRAME, ALIAS, MXE) are kept, not written; SRV and CAA
// wait for the manual pass (ticket 03) to confirm Namecheap's form shape
// for them. Any type can be deleted.
var writeTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS"}

// writable is an error naming each Record a Changeset adds or modifies to
// a type Apply does not write, or to NS at the apex; nil if there is none.
func writable(cs dns.Changeset) error {
	var bad []string

	for _, c := range cs.Changes {
		r := c.After
		if c.Kind == dns.ChangeDelete {
			continue
		}

		if !slices.Contains(writeTypes, r.Type) || (r.Type == "NS" && provider.RelativeName(cs.Zone.Name, r.Name) == "@") {
			bad = append(bad, r.Type+" "+r.Name)
		}
	}

	if bad == nil {
		return nil
	}

	return fmt.Errorf("namecheap: dnscli writes only A, AAAA, CNAME, MX, TXT and NS below the apex (SRV and CAA writes wait for a check against Namecheap), not %s; nothing was applied",
		strings.Join(bad, ", "))
}

// mailWritable is an error when a Changeset adds or modifies an MX Record
// of a domain whose EmailType is not MX ("Custom MX" in Namecheap's
// console): Namecheap ignores MX hosts then. Apply keeps the EmailType
// (mail forwarding or Private Email stays as set up), so the user changes
// it in the console. Nil otherwise.
func mailWritable(cs dns.Changeset, emailType string) error {
	if strings.EqualFold(emailType, "MX") {
		return nil
	}

	for _, c := range cs.Changes {
		if c.Kind != dns.ChangeDelete && c.After.Type == "MX" {
			return fmt.Errorf("namecheap: %s's mail setting is %q, so Namecheap ignores MX records; set it to \"Custom MX\" in Namecheap's console to write %s %s; nothing was applied",
				cs.Zone.Name, emailType, c.After.Type, c.After.Name)
		}
	}

	return nil
}

// read is a host as sent, as Records reads it back.
func (h sendHost) read(zone string) dns.Record {
	pref, _ := strconv.ParseUint(h.MXPref, 10, 16)

	return toRecord(zone, wireHost{Name: h.Name, Type: h.Type, Address: h.Address, MXPref: uint32(pref), TTL: h.TTL})
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
	h := sendHost{Name: provider.RelativeName(zone, r.Name), Type: r.Type, Address: r.Value, TTL: r.TTL}

	switch r.Type {
	case "CNAME", "NS":
		h.Address = fqdn(r.Value)
	case "MX":
		f := strings.Fields(r.Value)
		if len(f) != 2 {
			return sendHost{}, fmt.Errorf("namecheap: MX value %q is not \"priority host\"; nothing was applied", r.Value)
		}

		if _, err := strconv.ParseUint(f[0], 10, 16); err != nil {
			return sendHost{}, fmt.Errorf("namecheap: MX value %q is not \"priority host\"; nothing was applied", r.Value)
		}

		h.MXPref, h.Address = f[0], fqdn(f[1])
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

	// An ERROR answer (StatusCode 0) is Namecheap refusing the write. Any
	// other failure may come after Namecheap took it.
	var apiErr *provider.APIError

	switch {
	case errors.As(err, &apiErr) && apiErr.StatusCode == 0:
		return fmt.Errorf("%w; nothing was applied", err)
	case err != nil:
		return fmt.Errorf("%w; Namecheap may or may not have applied the changes, Refresh the Zone", err)
	case !res.Result.IsSuccess:
		return errors.New("namecheap: setHosts answered without success; Namecheap may or may not have applied the changes, Refresh the Zone")
	}

	return nil
}
