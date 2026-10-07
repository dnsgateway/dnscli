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
	// Resolver is the system DNS resolver the Serial Checks and the
	// Delegation lookups ask. Nil runs neither.
	Resolver Resolver
	// SerialChecks is how many Serial Checks may run at once, and, apart
	// from them, how many Delegation lookups. Defaults to
	// DefaultSerialChecks.
	SerialChecks int
	// Refreshes is how many Refreshes may run at once. Defaults to
	// DefaultRefreshes.
	Refreshes int
	// UnsealMethod is the session's Unseal Method. Defaults to the
	// passphrase method with the default argon2id cost.
	UnsealMethod vault.Method
	// Passphrase is the passphrase Unseal Method, used when the session's
	// method asks for no secret, such as the Keychain: it enrolls the
	// first data key, for recovery, and Unseals when the Keychain cannot.
	// Defaults to the passphrase method with the default argon2id cost.
	Passphrase vault.Method
	// Keychain is the Keychain Unseal Method that an unsealed session can
	// enroll; nil where the platform has none.
	Keychain vault.Method
}

// Resolver answers DNS queries through the system resolver. Its answers
// are hints only: a caching resolver can lag behind the Provider and
// follows public Delegation (ADR-0001).
type Resolver interface {
	// SOA returns the SOA serial the resolver gives for a zone name.
	SOA(ctx context.Context, zone string) (serial uint32, err error)
	// NS returns the nameservers the resolver gives for a zone name: the
	// Delegation, as far as the resolver shows it.
	NS(ctx context.Context, zone string) (nameservers []string, err error)
}

// DefaultSerialChecks is the default limit on Serial Checks running at once.
const DefaultSerialChecks = 8

func (d Deps) serialChecks() int {
	if d.SerialChecks < 1 {
		return DefaultSerialChecks
	}

	return d.SerialChecks
}

// DefaultRefreshes is the default limit on Refreshes running at once.
const DefaultRefreshes = 4

func (d Deps) refreshes() int {
	if d.Refreshes < 1 {
		return DefaultRefreshes
	}

	return d.Refreshes
}

func (d Deps) unsealMethod() vault.Method {
	if d.UnsealMethod == nil {
		return vault.Passphrase{}
	}

	return d.UnsealMethod
}

// passphrase returns the method the Unseal prompt types a secret into: the
// session's method when it asks for one, else the passphrase method.
func (d Deps) passphrase() vault.Method {
	switch {
	case vault.AsksForSecret(d.unsealMethod()):
		return d.unsealMethod()
	case d.Passphrase != nil:
		return d.Passphrase
	}

	return vault.Passphrase{}
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
