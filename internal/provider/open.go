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

	tenants     memo[string]
	nameservers memo[[]string]
}

// NewOpener returns an Opener of the Providers in reg, each built with
// opts.
func NewOpener(reg dnsprovider.Registry, opts ...dnsprovider.Option) *Opener {
	return &Opener{reg: reg, opts: opts}
}

// Open builds the Provider of type typ for an Account from its field
// map, as Credentials reads it. It does not change fields.
//
// A Provider that lists Tenants and takes one in a field marked Tenant is
// scoped to the Tenant stored in that field. Without one, it is scoped to
// the only Tenant the credentials reach; credentials that reach none or
// several, or may not list them, leave it unscoped, acting on every Zone
// they reach. The outcome is kept for the credentials, so the Tenants are
// listed once.
func (o *Opener) Open(ctx context.Context, typ string, fields map[string]string) (dnsprovider.Provider, error) {
	f, err := o.factory(typ)
	if err != nil {
		return nil, err
	}

	fields = maps.Clone(fields)

	i := slices.IndexFunc(f.Fields, func(fd dnsprovider.Field) bool { return fd.Tenant })
	if i < 0 || fields[f.Fields[i].Name] != "" {
		return f.New(fields, o.opts...)
	}

	tenantField, key := f.Fields[i].Name, credentialsKey(typ, fields)

	if id, ok := o.tenants.kept(key); ok {
		if id != "" {
			fields[tenantField] = id
		}

		return f.New(fields, o.opts...)
	}

	p, err := f.New(fields, o.opts...)
	if err != nil {
		return nil, err
	}

	lister, ok := p.(dnsprovider.TenantLister)
	if !ok {
		return p, nil
	}

	id, _ := o.tenants.get(ctx, key, func() (string, bool) {
		id := onlyTenant(ctx, lister)

		// A cancelled list says nothing of the credentials.
		return id, ctx.Err() == nil
	})
	if id == "" {
		return p, nil
	}

	fields[tenantField] = id

	return f.New(fields, o.opts...)
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

// onlyTenant is the one Tenant credentials reach; empty when they reach
// none or several, or may not list them.
func onlyTenant(ctx context.Context, l dnsprovider.TenantLister) string {
	list, err := l.Tenants(ctx)
	if err != nil || len(list) != 1 {
		return ""
	}

	return list[0].ID
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
