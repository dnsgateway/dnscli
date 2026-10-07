// Package dns holds dnscli's domain types: Account, Zone and Record.
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
