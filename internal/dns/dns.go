// Package dns holds dnscli's cache types: Account, and the Zone, Record
// and Changeset as the cache holds them, with database IDs, Refresh times
// and the cached Zone Status. The dnsprovider module owns the Provider
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
	// Nameservers are the nameservers the Zone's Account expects its
	// Delegation to name, as the Provider assigned them to this Zone; nil
	// when not known.
	Nameservers []string

	// Status is the cached Provider-side Zone Status.
	Status dnsprovider.ZoneStatus
	// StatusAt is when Status was read; zero when it never was. A Refresh
	// that could not read it leaves it older than RefreshedAt.
	StatusAt time.Time
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
