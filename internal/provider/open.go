// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

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
	// every keeps the unscoped Provider of credentials reaching several
	// Tenants, which lists the Zones of every one.
	every memo[dnsprovider.Provider]
}

// NewOpener returns an Opener of the Providers in reg, each built with
// opts.
func NewOpener(reg dnsprovider.Registry, opts ...dnsprovider.Option) *Opener {
	return &Opener{reg: reg, opts: opts}
}

// Open builds the Provider of type typ for an Account from its field
// map, as Credentials reads it, scoped as OpenScoped finds. It does not
// change fields.
func (o *Opener) Open(ctx context.Context, typ string, fields map[string]string) (dnsprovider.Provider, error) {
	p, _, err := o.OpenScoped(ctx, typ, fields)

	return p, err
}

// OpenScoped builds the Provider of type typ for an Account from its
// field map, as Credentials reads it, and returns the field map it was
// built from: fields, with the Tenant set for a Provider that lists
// Tenants and takes one in a field marked Tenant. A Tenant already stored
// in that field is kept, and the Tenants are not listed. Without one, it
// is the only Tenant the credentials reach; credentials that reach none,
// or may not list them, are left unscoped, acting on every Zone they
// reach, and ones that reach several are a *TenantChoiceError, without
// building a Provider for any. The Tenants are listed once per
// credentials, and the Provider that listed them is the one returned
// when it is unscoped. It does not change fields.
func (o *Opener) OpenScoped(ctx context.Context, typ string, fields map[string]string) (dnsprovider.Provider, map[string]string, error) {
	scoped, p, err := o.scope(ctx, typ, fields)
	if err != nil || p != nil {
		return p, scoped, err
	}

	p, err = o.build(typ, scoped)

	return p, scoped, err
}

// build builds the Provider of type typ from fields as they are. Fields
// it cannot be built from are an InvalidCredentialsError.
func (o *Opener) build(typ string, fields map[string]string) (dnsprovider.Provider, error) {
	f, err := o.factory(typ)
	if err == nil {
		var p dnsprovider.Provider
		if p, err = f.New(fields, o.opts...); err == nil {
			return p, nil
		}
	}

	return nil, &InvalidCredentialsError{Err: err}
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

// scope is the field map OpenScoped builds the Provider from, and the
// Provider it built to list the Tenants when that one is unscoped as the
// fields are: it is opened already. Otherwise the Provider is nil.
func (o *Opener) scope(ctx context.Context, typ string, fields map[string]string) (map[string]string, dnsprovider.Provider, error) {
	tenantField, err := o.tenantField(typ)
	if err != nil {
		return nil, nil, err
	}

	fields = maps.Clone(fields)

	if tenantField == "" || fields[tenantField] != "" {
		return fields, nil, nil
	}

	tenants, built, err := o.listTenants(ctx, typ, fields)
	if err != nil {
		return nil, nil, err
	}

	switch len(tenants) {
	case 0:
		return fields, built, nil
	case 1:
		fields[tenantField] = tenants[0].ID

		return fields, nil, nil
	}

	return nil, nil, &TenantChoiceError{Tenants: slices.Clone(tenants)}
}

// listTenants lists the Tenants credentials reach, given as fields
// without a Tenant, once per credentials, and returns the Provider built
// to list them when it was built now; nil when they were listed before.
// Credentials that may not list them reach none.
func (o *Opener) listTenants(ctx context.Context, typ string, fields map[string]string) ([]dnsprovider.Tenant, dnsprovider.Provider, error) {
	var built dnsprovider.Provider

	tenants, err := o.tenants.get(ctx, credentialsKey(typ, fields), func() ([]dnsprovider.Tenant, error) {
		var err error
		if built, err = o.build(typ, fields); err != nil {
			return nil, err
		}

		if !dnsprovider.Can(built, dnsprovider.CapTenantLister) {
			return nil, nil
		}

		// Credentials that may not list Tenants have none to choose
		// from; any other failure says nothing of them.
		list, err := built.ListTenants(ctx)
		if errors.Is(err, dnsprovider.ErrUnauthorized) {
			return nil, nil
		}

		return list, err
	})

	return tenants, built, err
}

// tenantField is the field Provider type typ takes its Tenant in; empty
// when it takes none.
func (o *Opener) tenantField(typ string) (string, error) {
	f, err := o.factory(typ)
	if err != nil {
		return "", &InvalidCredentialsError{Err: err}
	}

	return TenantField(f), nil
}

// EveryTenant is the Provider of type typ acting on the Zones of every
// Tenant an Account's credentials reach, given as the field map
// OpenScoped returned: built unscoped when they reach several, and kept
// for them; nil when the Provider OpenScoped built does, as they reach
// one Tenant, or none. An Account's Zones are those of every Tenant it
// reaches; its own Tenant is the one it Onboards in. err is why the
// Tenants could not be listed: whether they are several is then unknown.
func (o *Opener) EveryTenant(ctx context.Context, typ string, fields map[string]string) (dnsprovider.Provider, error) {
	tenantField, err := o.tenantField(typ)
	if err != nil || tenantField == "" || fields[tenantField] == "" {
		return nil, err
	}

	unscoped := maps.Clone(fields)
	delete(unscoped, tenantField)

	tenants, built, err := o.listTenants(ctx, typ, unscoped)
	if err != nil || len(tenants) < 2 {
		return nil, err
	}

	return o.every.get(ctx, credentialsKey(typ, unscoped), func() (dnsprovider.Provider, error) {
		if built != nil {
			return built, nil
		}

		return o.build(typ, unscoped)
	})
}

// namedTenant is the Tenant verified credentials act within, as their
// Account named it: tenant, else the one set in scoped, named as the
// credentials, given as fields, listed it.
func (o *Opener) namedTenant(typ string, fields, scoped map[string]string, tenant dnsprovider.Tenant) dnsprovider.Tenant {
	f, _ := o.factory(typ)

	tenantField := TenantField(f)
	if tenantField == "" {
		return tenant
	}

	if tenant.ID == "" {
		tenant.ID = scoped[tenantField]
	}

	if listed, ok := o.tenants.kept(credentialsKey(typ, fields)); ok && tenant.Name == "" {
		if j := slices.IndexFunc(listed, func(t dnsprovider.Tenant) bool { return t.ID == tenant.ID }); j >= 0 {
			tenant.Name = listed[j].Name
		}
	}

	return tenant
}

// TenantField is the name of the field f's Provider takes its Tenant in,
// the one marked Tenant; empty when it takes none.
func TenantField(f dnsprovider.Factory) string {
	if i := slices.IndexFunc(f.Fields, func(fd dnsprovider.Field) bool { return fd.Tenant }); i >= 0 {
		return f.Fields[i].Name
	}

	return ""
}

// factory is the registered Factory of Provider type typ.
func (o *Opener) factory(typ string) (dnsprovider.Factory, error) {
	f, ok := o.reg[typ]
	if !ok || f.New == nil {
		return dnsprovider.Factory{}, fmt.Errorf("Provider type %q is not available in this build", typ)
	}

	return f, nil
}

// InvalidCredentialsError is a field map no Provider could be built
// from, such as one missing a field or holding a malformed one, or of a
// Provider type not in this build: the Provider was not asked.
type InvalidCredentialsError struct{ Err error }

func (e *InvalidCredentialsError) Error() string { return e.Err.Error() }

func (e *InvalidCredentialsError) Unwrap() error { return e.Err }

// Verify builds the Provider of type typ from fields as Open does,
// scoped to the Tenant OpenScoped finds, and asks it for its Account, which
// proves the credentials work: a Tenant-owned token is only verified
// within its Tenant. It is the check made before an Account is saved, and
// returns the credentials to save, with that Tenant set, and the Tenant
// they act within; empty when unscoped. Fields the Provider cannot be
// built from are an InvalidCredentialsError, and credentials reaching
// several Tenants a *TenantChoiceError, without asking the Provider. The
// nameservers the Account expects are kept for the credentials returned,
// as Nameservers would keep them.
func (o *Opener) Verify(ctx context.Context, typ string, fields map[string]string) (map[string]string, dnsprovider.Tenant, error) {
	p, scoped, err := o.OpenScoped(ctx, typ, fields)
	if err != nil {
		return nil, dnsprovider.Tenant{}, err
	}

	info, err := p.Account(ctx)
	if err != nil {
		return nil, dnsprovider.Tenant{}, err
	}

	_, _ = o.nameservers.get(ctx, credentialsKey(typ, scoped), func() ([]string, error) { return info.Nameservers, nil })

	return scoped, o.namedTenant(typ, fields, scoped, info.Tenant), nil
}

// Nameservers are the nameservers the Account of p, opened with typ and
// fields, expects Delegation to name, from its Account call. They are
// asked once per credentials: a failure is not kept, and is asked again.
func (o *Opener) Nameservers(ctx context.Context, p dnsprovider.Provider, typ string, fields map[string]string) ([]string, error) {
	ns, err := o.nameservers.get(ctx, credentialsKey(typ, fields), func() ([]string, error) {
		info, err := p.Account(ctx)

		return info.Nameservers, err
	})

	return slices.Clone(ns), err
}

// credentialsKey keys what is kept for an Account's credentials without
// holding them.
func credentialsKey(typ string, fields map[string]string) string {
	// A field set empty is one left out: a Tenant field stored empty
	// keys as one missing.
	fields = maps.Clone(fields)
	maps.DeleteFunc(fields, func(_, v string) bool { return v == "" })

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
	err  error
}

// kept is the value kept for key, if any.
func (c *memo[T]) kept(key string) (T, bool) {
	c.mu.Lock()
	e, found := c.m[key]
	c.mu.Unlock()

	if found {
		select {
		case <-e.done:
			if e.err == nil {
				return e.v, true
			}
		default:
		}
	}

	var zero T

	return zero, false
}

// get is the value kept for key, else fn's, which is kept unless fn
// fails. Callers asking while fn runs wait for its value, or its error; a
// caller whose ctx ends first gets ctx's error. A waiter whose fn ended
// with its own caller's context asks again.
func (c *memo[T]) get(ctx context.Context, key string, fn func() (T, error)) (T, error) {
	var zero T

	for {
		c.mu.Lock()

		e, found := c.m[key]
		if !found {
			break
		}

		c.mu.Unlock()

		select {
		case <-e.done:
		case <-ctx.Done():
			return zero, ctx.Err()
		}

		if e.err == nil || ctx.Err() != nil ||
			(!errors.Is(e.err, context.Canceled) && !errors.Is(e.err, context.DeadlineExceeded)) {
			return e.v, e.err
		}
	}

	e := &memoEntry[T]{done: make(chan struct{})}
	if c.m == nil {
		c.m = map[string]*memoEntry[T]{}
	}

	c.m[key] = e
	c.mu.Unlock()

	e.v, e.err = fn()

	if e.err != nil {
		c.mu.Lock()
		delete(c.m, key)
		c.mu.Unlock()
	}

	close(e.done)

	return e.v, e.err
}
