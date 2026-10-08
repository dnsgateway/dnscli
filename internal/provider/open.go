package provider

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/dnsgateway/dnsprovider"
)

// Opener builds the Providers of Accounts from a Registry, and keeps, for
// each Account's credentials, what it had to ask the Provider: the Tenant
// they are scoped to and the nameservers their Account expects. Each is
// asked once per credentials for the Opener's life, however many Opens
// run at once.
type Opener struct {
	reg  dnsprovider.Registry
	opts []dnsprovider.Option

	tenants     memo[[]dnsprovider.Tenant]
	nameservers memo[[]string]
}

// NewOpener returns an Opener of the Providers in reg, each built with
// opts.
func NewOpener(reg dnsprovider.Registry, opts ...dnsprovider.Option) *Opener {
	return &Opener{reg: reg, opts: opts}
}

// Open builds the Provider of type typ for an Account from its field
// map, as Credentials reads it, scoped as Scope finds. It does not change
// fields.
func (o *Opener) Open(ctx context.Context, typ string, fields map[string]string) (dnsprovider.Provider, error) {
	scoped, p, err := o.scope(ctx, typ, fields)
	if err != nil || p != nil {
		return p, err
	}

	f, err := o.factory(typ)
	if err != nil {
		return nil, err
	}

	return f.New(scoped, o.opts...)
}

// TenantChoiceError is credentials that reach several Tenants, none of
// which is stored with them: one must be chosen before the Provider is
// opened.
type TenantChoiceError struct {
	Tenants []dnsprovider.Tenant
}

func (e *TenantChoiceError) Error() string {
	return fmt.Sprintf("the credentials reach %d Tenants: choose the one the Account acts within", len(e.Tenants))
}

// Scope is the field map an Account's Provider is opened with: fields,
// with the Tenant set for a Provider that lists Tenants and takes one in
// a field marked Tenant. A Tenant already stored in that field is kept,
// and the Tenants are not listed. Without one, it is the only Tenant the
// credentials reach; credentials that reach none, or may not list them,
// are left unscoped, acting on every Zone they reach, and ones that reach
// several are a *TenantChoiceError. The Tenants are listed once per
// credentials. It does not change fields.
func (o *Opener) Scope(ctx context.Context, typ string, fields map[string]string) (map[string]string, error) {
	scoped, _, err := o.scope(ctx, typ, fields)

	return scoped, err
}

// scope is Scope, which also returns the Provider it built to list the
// Tenants when that one is unscoped as the fields are: it is opened
// already. Otherwise the Provider is nil.
func (o *Opener) scope(ctx context.Context, typ string, fields map[string]string) (map[string]string, dnsprovider.Provider, error) {
	f, err := o.factory(typ)
	if err != nil {
		return nil, nil, err
	}

	fields = maps.Clone(fields)

	i := slices.IndexFunc(f.Fields, func(fd dnsprovider.Field) bool { return fd.Tenant })
	if i < 0 || fields[f.Fields[i].Name] != "" {
		return fields, nil, nil
	}

	var built dnsprovider.Provider

	tenants, ok := o.tenants.get(ctx, credentialsKey(typ, fields), func() ([]dnsprovider.Tenant, bool) {
		if built, err = f.New(fields, o.opts...); err != nil {
			return nil, false
		}

		lister, ok := built.(dnsprovider.TenantLister)
		if !ok {
			return nil, true
		}

		var list []dnsprovider.Tenant

		// Credentials that may not list Tenants have none to choose
		// from; any other failure says nothing of them.
		if list, err = lister.Tenants(ctx); errors.Is(err, dnsprovider.ErrUnauthorized) {
			return nil, true
		}

		return list, err == nil
	})
	if !ok {
		if err == nil {
			err = errors.New("the Tenants the credentials reach could not be listed")
			if ctxErr := ctx.Err(); ctxErr != nil {
				err = ctxErr
			}
		}

		return nil, nil, err
	}

	switch len(tenants) {
	case 0:
		return fields, built, nil
	case 1:
		fields[f.Fields[i].Name] = tenants[0].ID

		return fields, nil, nil
	}

	return nil, nil, &TenantChoiceError{Tenants: slices.Clone(tenants)}
}

// factory is the registered Factory of Provider type typ.
func (o *Opener) factory(typ string) (dnsprovider.Factory, error) {
	f, ok := o.reg[typ]
	if !ok || f.New == nil {
		return dnsprovider.Factory{}, fmt.Errorf("Provider type %q is not available in this build", typ)
	}

	return f, nil
}

// InvalidCredentialsError is a field map Verify could not build its
// Provider from, such as one missing a field or holding a malformed one:
// the Provider was not asked.
type InvalidCredentialsError struct{ Err error }

func (e *InvalidCredentialsError) Error() string { return e.Err.Error() }

func (e *InvalidCredentialsError) Unwrap() error { return e.Err }

// Verify builds the Provider of type typ from fields as Open does,
// scoped to the Tenant it finds, and asks it for its Account, which
// proves the credentials work: a Tenant-owned token is only verified
// within its Tenant. Fields the Provider cannot be built from are an
// InvalidCredentialsError. It is the check made before an Account is
// saved. The Tenant and the nameservers the Account expects are kept for
// the credentials, as Open and Nameservers would keep them.
func (o *Opener) Verify(ctx context.Context, typ string, fields map[string]string) error {
	p, err := o.Open(ctx, typ, fields)
	if err != nil {
		return &InvalidCredentialsError{Err: err}
	}

	info, err := p.Account(ctx)
	if err != nil {
		return err
	}

	o.nameservers.get(ctx, credentialsKey(typ, fields), func() ([]string, bool) { return info.Nameservers, true })

	return nil
}

// Nameservers are the nameservers the Account of p, opened with typ and
// fields, expects Delegation to name, from its Account call. They are
// asked once per credentials: a failure is not kept, and is asked again.
func (o *Opener) Nameservers(ctx context.Context, p dnsprovider.Provider, typ string, fields map[string]string) ([]string, error) {
	var err error

	ns, ok := o.nameservers.get(ctx, credentialsKey(typ, fields), func() ([]string, bool) {
		var info dnsprovider.AccountInfo

		info, err = p.Account(ctx)

		return info.Nameservers, err == nil
	})
	if !ok && err == nil {
		err = errors.New("the Account's nameservers could not be read")
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
	}

	return slices.Clone(ns), err
}

// credentialsKey keys what is kept for an Account's credentials without
// holding them.
func credentialsKey(typ string, fields map[string]string) string {
	sum := sha256.Sum256(append([]byte(typ+"\x00"), EncodeCredentials(fields)...))

	return string(sum[:])
}

// memo keeps one value per key, computed once for all callers asking at
// the same time.
type memo[T any] struct {
	mu sync.Mutex
	m  map[string]*memoEntry[T]
}

type memoEntry[T any] struct {
	done chan struct{}
	v    T
	ok   bool
}

// kept is the value kept for key, if any.
func (c *memo[T]) kept(key string) (T, bool) {
	c.mu.Lock()
	e, found := c.m[key]
	c.mu.Unlock()

	if found {
		select {
		case <-e.done:
			if e.ok {
				return e.v, true
			}
		default:
		}
	}

	var zero T

	return zero, false
}

// get is the value kept for key, else fn's, which is kept when fn reports
// ok. Callers asking while fn runs wait for its value; a caller whose ctx
// ends first gets none.
func (c *memo[T]) get(ctx context.Context, key string, fn func() (T, bool)) (T, bool) {
	c.mu.Lock()

	if e, found := c.m[key]; found {
		c.mu.Unlock()

		select {
		case <-e.done:
			return e.v, e.ok
		case <-ctx.Done():
			var zero T

			return zero, false
		}
	}

	e := &memoEntry[T]{done: make(chan struct{})}
	if c.m == nil {
		c.m = map[string]*memoEntry[T]{}
	}

	c.m[key] = e
	c.mu.Unlock()

	e.v, e.ok = fn()

	if !e.ok {
		c.mu.Lock()
		delete(c.m, key)
		c.mu.Unlock()
	}

	close(e.done)

	return e.v, e.ok
}
