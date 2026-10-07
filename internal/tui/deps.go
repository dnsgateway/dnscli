package tui

import (
	"time"

	"github.com/sergelogvinov/dnscli/internal/store"
)

// Deps are the model's injected dependencies. main wires the real
// implementations; tests wire fakes and a Store on a temp file (Seam 1).
type Deps struct {
	// Store is the local database the cached view renders from.
	Store *store.Store
	// Clock returns the current time. Defaults to time.Now.
	Clock func() time.Time

	// Providers builds a Provider for an Account's Provider type.
	// Placeholder until the Provider interface lands (Account discovery, Refresh).
	Providers ProviderRegistry
	// Resolver answers SOA and NS queries for Serial Checks and Delegation.
	// Placeholder until Serial Checks land.
	Resolver Resolver
	// UnsealMethods are the enrolled ways to Unseal the session.
	// Placeholder until the Vault lands.
	UnsealMethods []UnsealMethod
}

// ProviderRegistry maps a Provider type to a constructor. Not built yet.
type ProviderRegistry interface{}

// Resolver is the system DNS resolver used for Serial Checks. Not built yet.
type Resolver interface{}

// UnsealMethod is one way of performing Unseal. Not built yet.
type UnsealMethod interface{}

func (d Deps) now() time.Time {
	if d.Clock == nil {
		return time.Now()
	}

	return d.Clock()
}
