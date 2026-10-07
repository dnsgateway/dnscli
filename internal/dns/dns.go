// Package dns holds dnscli's domain types: Account, Zone, Record,
// Changeset and Zone Status.
// They are plain values with no Provider or UI dependencies.
package dns

import "time"

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
	Status ZoneStatus
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

// ZoneStatus is the Provider-side summary of a Zone shown in the status bar.
// Mail records and the Live Zone marker are worked out by dnscli itself.
type ZoneStatus struct {
	DNSSEC Setting
	HSTS   Setting
}

// Setting is a Zone setting that is on, off, pending a change between
// the two, or unknown, such as when the API token may not read it.
type Setting int8

// Settings. The zero value is Unknown. The values are stored in the cache:
// append new ones, never renumber.
const (
	Unknown Setting = iota
	Off
	On
	// Pending is a setting the Provider is still turning on or off, such
	// as DNSSEC waiting for its DS record at the registrar.
	Pending
)

// String is "on", "off", "pending" or "?".
func (s Setting) String() string {
	switch s {
	case On:
		return "on"
	case Off:
		return "off"
	case Pending:
		return "pending"
	}

	return "?"
}
