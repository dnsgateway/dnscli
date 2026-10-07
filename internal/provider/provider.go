// Package provider is dnscli's own, Changeset-based Provider interface
// (ADR-0003) and the registry that maps a Provider type to its constructor.
//
// Success semantics and rate limiting live inside each adapter.
package provider

import (
	"context"
	"maps"
	"slices"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// Provider is one Account at a DNS hosting service.
type Provider interface {
	// Zones lists the Account's Zones. Each has Name and ProviderID set.
	Zones(ctx context.Context) ([]dns.Zone, error)
	// Records lists a Zone's Records, with the SOA serial when available.
	Records(ctx context.Context, zone dns.Zone) (RecordSet, error)
	// Apply applies a Changeset and reports the result of each change.
	Apply(ctx context.Context, cs dns.Changeset) (ApplyResult, error)
	// ZoneStatus reads the Provider-side Zone Status.
	ZoneStatus(ctx context.Context, zone dns.Zone) (dns.ZoneStatus, error)
	// Nameservers are the nameservers the Account expects Delegation to name.
	Nameservers(ctx context.Context) ([]string, error)
}

// RecordSet is a Zone's Records as the Provider returned them.
type RecordSet struct {
	Records []dns.Record
	// Serial is the Zone's SOA serial; zero when the Provider does not report it.
	Serial uint32
}

// ChangeResult is the outcome of one change in a Changeset.
type ChangeResult struct {
	Change dns.Change
	// Record is the Record as the Provider stored it; empty for a deletion
	// and for a failed change.
	Record dns.Record
	// Err is nil when the Provider confirmed the change.
	Err error
}

// ApplyResult is the outcome of applying a Changeset.
type ApplyResult struct {
	// Results has one entry per change, in Changeset order.
	Results []ChangeResult
	// Serial is the Zone's new SOA serial; zero when not known.
	Serial uint32
}

// Constructor builds a Provider for an Account from its API token.
type Constructor func(account dns.Account, token string) (Provider, error)

// Registry maps a Provider type, such as "cloudflare", to its constructor.
type Registry map[string]Constructor

// Types returns the registered Provider types, sorted.
func (r Registry) Types() []string {
	return slices.Sorted(maps.Keys(r))
}
