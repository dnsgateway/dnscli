// Package fake is a scriptable, in-memory Provider for tests (Seam 1).
//
// It is scripted with Zones, Records and failures, and records every call
// made to it, including the Account and API token it was built with.
package fake

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// Op names a Provider operation, for scripted failures and recorded calls.
type Op string

// Operations.
const (
	OpNew         Op = "New"
	OpZones       Op = "Zones"
	OpRecords     Op = "Records"
	OpApply       Op = "Apply"
	OpZoneStatus  Op = "ZoneStatus"
	OpNameservers Op = "Nameservers"
)

// Call is one recorded call.
type Call struct {
	Op Op
	// Account and Token are set for OpNew.
	Account dns.Account
	Token   string
	// Zone is set for the Zone operations.
	Zone dns.Zone
	// Changeset is set for OpApply.
	Changeset dns.Changeset
}

// Provider is the fake. Its zero value is not usable; call New.
type Provider struct {
	mu          sync.Mutex
	zones       []dns.Zone
	records     map[string][]dns.Record // by Zone ProviderID
	serials     map[string]uint32
	status      map[string]dns.ZoneStatus
	nameservers []string
	fail        map[Op]error
	failChange  func(dns.Change) error
	withhold    bool
	noSerial    bool
	ttlRange    []uint32 // min, max; nil when not scripted
	proxiable   []string // nil when not scripted
	calls       []Call
	nextID      int
}

// New returns an empty fake Provider.
func New() *Provider {
	return &Provider{
		records: map[string][]dns.Record{},
		serials: map[string]uint32{},
		status:  map[string]dns.ZoneStatus{},
		fail:    map[Op]error{},
	}
}

// AddZone scripts a Zone, identified by its ProviderID, with its Records.
func (p *Provider) AddZone(z dns.Zone, records ...dns.Record) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.zones = append(p.zones, dns.Zone{Name: z.Name, ProviderID: z.ProviderID, Nameservers: slices.Clone(z.Nameservers)})
	p.records[z.ProviderID] = slices.Clone(records)
	p.serials[z.ProviderID] = z.Serial

	return p
}

// SetRecords replaces a scripted Zone's Records, as if they were changed
// outside dnscli.
func (p *Provider) SetRecords(zoneProviderID string, records ...dns.Record) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.records[zoneProviderID] = slices.Clone(records)
}

// SetTTLRange makes the Providers New builds report a TTL range through
// provider.TTLLimits. Unscripted, they do not implement it.
func (p *Provider) SetTTLRange(minTTL, maxTTL uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.ttlRange = []uint32{minTTL, maxTTL}
}

// SetProxiableTypes makes the Providers New builds proxy Records of these
// types, through provider.ProxyFlag. Unscripted, they do not implement it.
func (p *Provider) SetProxiableTypes(types ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.proxiable = slices.Clone(types)
}

// withOptions is the fake with its scripted optional interfaces.
type withOptions struct {
	*Provider
}

// TTLRange implements provider.TTLLimits; the default range when not
// scripted.
func (p withOptions) TTLRange() (minTTL, maxTTL uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ttlRange == nil {
		return dns.DefaultTTLRange.Min, dns.DefaultTTLRange.Max
	}

	return p.ttlRange[0], p.ttlRange[1]
}

// ProxiableTypes implements provider.ProxyFlag.
func (p withOptions) ProxiableTypes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.proxiable)
}

// SetZoneStatus scripts a Zone's Zone Status.
func (p *Provider) SetZoneStatus(zoneProviderID string, s dns.ZoneStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.status[zoneProviderID] = s
}

// SetNameservers scripts the Account's expected nameservers.
func (p *Provider) SetNameservers(ns ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.nameservers = ns
}

// Fail makes every later call of op fail with err; a nil err clears it.
func (p *Provider) Fail(op Op, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err == nil {
		delete(p.fail, op)

		return
	}

	p.fail[op] = err
}

// FailChanges makes Apply fail each change for which fn returns an error,
// while applying the rest: a partial failure.
func (p *Provider) FailChanges(fn func(dns.Change) error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.failChange = fn
}

// WithholdRecords makes Apply report no stored Record for the changes it
// confirms, like a Provider whose API does not return them.
func (p *Provider) WithholdRecords(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.withhold = on
}

// WithholdSerial makes Records and Apply report no SOA serial, like a
// Provider whose API has no easy way to read it. The scripted serial
// still goes up with each Apply.
func (p *Provider) WithholdSerial(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.noSerial = on
}

// serial is the serial Records and Apply report for a Zone.
func (p *Provider) serial(zoneProviderID string) uint32 {
	if p.noSerial {
		return 0
	}

	return p.serials[zoneProviderID]
}

// Calls returns the calls made so far, in order.
func (p *Provider) Calls() []Call {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.calls)
}

// New is the provider.Constructor for the fake: every Account it builds
// shares this fake's script.
func (p *Provider) New(account dns.Account, token string) (provider.Provider, error) {
	if err := p.record(Call{Op: OpNew, Account: account, Token: token}); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ttlRange != nil || p.proxiable != nil {
		return withOptions{p}, nil
	}

	return p, nil
}

// record logs a call and returns its scripted failure, if any.
func (p *Provider) record(c Call) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls = append(p.calls, c)

	return p.fail[c.Op]
}

// Zones implements provider.Provider.
func (p *Provider) Zones(context.Context) ([]dns.Zone, error) {
	if err := p.record(Call{Op: OpZones}); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.zones), nil
}

// Records implements provider.Provider.
func (p *Provider) Records(_ context.Context, z dns.Zone) (provider.RecordSet, error) {
	if err := p.record(Call{Op: OpRecords, Zone: z}); err != nil {
		return provider.RecordSet{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	records, ok := p.records[z.ProviderID]
	if !ok {
		return provider.RecordSet{}, fmt.Errorf("zone %s (%s) not found", z.Name, z.ProviderID)
	}

	return provider.RecordSet{Records: slices.Clone(records), Serial: p.serial(z.ProviderID)}, nil
}

// Apply implements provider.Provider. It applies each change to the
// scripted Records, unless FailChanges fails it, and bumps the serial when
// any change succeeded.
func (p *Provider) Apply(_ context.Context, cs dns.Changeset) (provider.ApplyResult, error) {
	if err := p.record(Call{Op: OpApply, Zone: cs.Zone, Changeset: cs}); err != nil {
		return provider.ApplyResult{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	zid := cs.Zone.ProviderID

	records, ok := p.records[zid]
	if !ok {
		return provider.ApplyResult{}, fmt.Errorf("zone %s (%s) not found", cs.Zone.Name, zid)
	}

	var res provider.ApplyResult

	for _, c := range cs.Changes {
		r := provider.ChangeResult{Change: c}
		if p.failChange != nil {
			r.Err = p.failChange(c)
		}

		if r.Err == nil {
			records, r.Record, r.Err = p.applyChange(records, c)
		}

		if p.withhold {
			r.Record = dns.Record{}
		}

		res.Results = append(res.Results, r)
	}

	p.records[zid] = records

	for _, r := range res.Results {
		if r.Err == nil {
			p.serials[zid]++
			res.Serial = p.serial(zid)

			break
		}
	}

	return res, nil
}

func (p *Provider) applyChange(records []dns.Record, c dns.Change) ([]dns.Record, dns.Record, error) {
	if c.Kind == dns.ChangeAdd {
		p.nextID++
		r := c.After
		r.ProviderID = fmt.Sprintf("fake-%d", p.nextID)

		return append(records, r), r, nil
	}

	i := slices.IndexFunc(records, func(r dns.Record) bool { return r.ProviderID == c.Before.ProviderID })
	if i < 0 {
		return records, dns.Record{}, fmt.Errorf("record %s not found", c.Before.ProviderID)
	}

	if c.Kind == dns.ChangeDelete {
		return slices.Delete(records, i, i+1), dns.Record{}, nil
	}

	r := c.After
	r.ProviderID = c.Before.ProviderID
	records[i] = r

	return records, r, nil
}

// ZoneStatus implements provider.Provider.
func (p *Provider) ZoneStatus(_ context.Context, z dns.Zone) (dns.ZoneStatus, error) {
	if err := p.record(Call{Op: OpZoneStatus, Zone: z}); err != nil {
		return dns.ZoneStatus{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.status[z.ProviderID], nil
}

// Nameservers implements provider.Provider.
func (p *Provider) Nameservers(context.Context) ([]string, error) {
	if err := p.record(Call{Op: OpNameservers}); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.nameservers), nil
}
