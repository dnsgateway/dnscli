package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// Deps are the model's injected dependencies. main wires the real
// implementations; tests wire fakes and a Store on a temp file (Seam 1).
type Deps struct {
	// Store is the local database the cached view renders from.
	Store *store.Store
	// Clock returns the current time. Defaults to time.Now.
	Clock func() time.Time
	// Shortcuts maps keys to actions. Defaults to DefaultShortcutProfile,
	// which also replaces a profile that fails Validate.
	Shortcuts *ShortcutProfile

	// Providers builds a Provider for an Account from its Provider type.
	Providers provider.Registry
	// Resolver is the system DNS resolver the Serial Checks ask. Nil runs
	// no Serial Checks.
	Resolver Resolver
	// SerialChecks is how many Serial Checks may run at once. Defaults to
	// DefaultSerialChecks.
	SerialChecks int
	// UnsealMethod is the session's Unseal Method. Defaults to the
	// passphrase method with the default argon2id cost.
	UnsealMethod vault.Method
}

// Resolver answers DNS queries through the system resolver. It is a
// freshness hint only: a caching resolver can lag behind the Provider and
// follows public Delegation (ADR-0001). Delegation (NS) lookups join it later.
type Resolver interface {
	// SOA returns the SOA serial the resolver gives for a zone name.
	SOA(ctx context.Context, zone string) (serial uint32, err error)
}

// DefaultSerialChecks is the default limit on Serial Checks running at once.
const DefaultSerialChecks = 8

func (d Deps) serialChecks() int {
	if d.SerialChecks < 1 {
		return DefaultSerialChecks
	}

	return d.SerialChecks
}

func (d Deps) unsealMethod() vault.Method {
	if d.UnsealMethod == nil {
		return vault.Passphrase{}
	}

	return d.UnsealMethod
}

// shortcuts returns the Shortcut Profile to use. An invalid injected profile
// is replaced by the default, and the error says why.
func (d Deps) shortcuts() (ShortcutProfile, error) {
	if d.Shortcuts == nil {
		return DefaultShortcutProfile(), nil
	}

	if err := d.Shortcuts.Validate(); err != nil {
		return DefaultShortcutProfile(), fmt.Errorf("Shortcut Profile %s: %w; using the default", d.Shortcuts.Name, err)
	}

	return *d.Shortcuts, nil
}

func (d Deps) now() time.Time {
	if d.Clock == nil {
		return time.Now()
	}

	return d.Clock()
}
