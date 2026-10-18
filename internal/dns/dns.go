// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package dns holds dnscli's cache types: Account, and the Zone, Record
// and Changeset as the cache holds them, with database IDs, Refresh times
// the cached Hosting State and Zone Status. The dnsprovider module owns the Provider
// side; internal/provider converts between the two.
package dns

import (
	"time"

	"github.com/dnsgateway/dnsprovider"
)

// Account is one set of credentials at a Provider.
type Account struct {
	ID       int64
	Provider string // Provider type, e.g. "cloudflare"
	Name     string // e.g. "work"
}

// Label is the user-facing name of the Account, `<provider>.<account>`.
func (a Account) Label() string {
	return a.Provider + "." + a.Name
}

// Zone is a DNS zone hosted in one Account.
type Zone struct {
	ID         int64
	AccountID  int64
	Name       string // zone name, e.g. "example.com"
	ProviderID string // Provider-assigned zone ID, opaque to dnscli

	// Serial is the cached SOA serial; zero when unknown.
	Serial uint32
	// RefreshedAt is the time of the last Refresh; zero when never Refreshed.
	RefreshedAt time.Time
	// State is the Zone's Hosting State as its Provider last listed it;
	// Unknown until then.
	State dnsprovider.HostingState
	// Nameservers are the nameservers the Zone's Account expects its
	// Delegation to name, as the Provider assigned them to this Zone; nil
	// when not known.
	Nameservers []string

	// Status is the cached Provider-side Zone Status.
	Status dnsprovider.ZoneStatus
	// StatusAt is when Status was read; zero when it never was. A Refresh
	// that could not read it leaves it older than RefreshedAt.
	StatusAt time.Time
	// SigningSeenAt is when dnscli first saw the Zone signed, its keys
	// read; zero when it has not, or has seen it unsigned since. The DS is
	// published only once resolvers may have flushed its unsigned data.
	SigningSeenAt time.Time
	// DSSeen is what dnscli saw of the DS the parent serves for the Zone.
	DSSeen DSSeen
}

// DSSeen is what dnscli saw of the DS a signed Zone's parent serves. The
// Zone is unsigned only once resolvers may have flushed the DS.
type DSSeen struct {
	// GoneAt is when dnscli first saw the parent serve no DS for the
	// signed Zone; zero when it has not, or has seen a DS or the Zone
	// unsigned since.
	GoneAt time.Time
	// TTL is the TTL of the DS set the parent last served, as last seen;
	// zero when none was seen.
	TTL time.Duration
}

// Record is a single DNS resource record inside a Zone.
type Record struct {
	ProviderID string // Provider-assigned record ID, opaque to dnscli
	Type       string
	Name       string
	Value      string
	TTL        uint32
	// Extras holds Provider-specific fields, such as Cloudflare's proxy flag.
	Extras map[string]string
}

// ChangeKind says what a Change does to a Record.
type ChangeKind int

// Change kinds.
const (
	ChangeAdd ChangeKind = iota + 1
	ChangeModify
	ChangeDelete
)

// Change is one Record addition, modification or deletion.
type Change struct {
	Kind ChangeKind
	// Before is the Record as the Changeset was built from it; empty for an addition.
	Before Record
	// After is the Record as it should become; empty for a deletion.
	After Record
}

// Changeset is a set of Record additions, modifications and deletions for
// one Zone, applied to the Provider as one operation.
type Changeset struct {
	Zone    Zone
	Changes []Change
}
