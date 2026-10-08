package provider

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providers/godaddy"
	"github.com/dnsgateway/dnsprovider/providers/namecheap"
)

// Opener builds the Providers of Accounts from a Registry, and keeps, for
// each API token, what it had to ask the Provider: the Tenant the token is
// scoped to and the nameservers its Account expects. Each is asked once
// per token for the Opener's life, however many Opens run at once.
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

// Open builds the Provider of type typ for an Account from its stored API
// token, read into the Provider's fields as Fields reads it.
//
// A Provider that lists Tenants and takes one in a field marked Tenant is
// scoped to the only Tenant the token reaches. A token that reaches none
// or several, or may not list them, leaves it unscoped, acting on every
// Zone the token reaches, since dnscli stores no Tenant yet. The outcome
// is kept for the token, so the Tenants are listed once.
func (o *Opener) Open(ctx context.Context, typ, token string) (dnsprovider.Provider, error) {
	f, ok := o.reg[typ]
	if !ok || f.New == nil {
		return nil, fmt.Errorf("Provider type %q is not available in this build", typ)
	}

	fields, err := Fields(typ, f.Fields, token)
	if err != nil {
		return nil, err
	}

	i := slices.IndexFunc(f.Fields, func(fd dnsprovider.Field) bool { return fd.Tenant })
	if i < 0 {
		return f.New(fields, o.opts...)
	}

	tenantField, key := f.Fields[i].Name, tokenKey(typ, token)

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

		// A cancelled list says nothing of the token.
		return id, ctx.Err() == nil
	})
	if id == "" {
		return p, nil
	}

	fields[tenantField] = id

	return f.New(fields, o.opts...)
}

// Nameservers are the nameservers the Account of p, opened with typ and
// token, expects Delegation to name, from its Account call. They are
// asked once per token: a failure is not kept, and is asked again.
func (o *Opener) Nameservers(ctx context.Context, p dnsprovider.Provider, typ, token string) ([]string, error) {
	var err error

	ns, ok := o.nameservers.get(ctx, tokenKey(typ, token), func() ([]string, bool) {
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

// tokenKey keys what is kept for a token without holding the token itself.
func tokenKey(typ, token string) string {
	sum := sha256.Sum256([]byte(typ + "\x00" + token))

	return string(sum[:])
}

// onlyTenant is the one Tenant a token reaches; empty when it reaches
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

// Shapes of the old single-token credentials that hold several fields.
var (
	errGoDaddyShape   = errors.New("godaddy: the API token is the key and secret as KEY:SECRET")
	errNamecheapShape = errors.New("namecheap: the API token is the API user, API key and whitelisted IPv4 client IP as APIUSER:APIKEY:CLIENTIP")
)

// Fields reads an Account's stored API token into the field map its
// Provider is built from. GoDaddy's token is KEY:SECRET, Namecheap's
// APIUSER:APIKEY:CLIENTIP; any other Provider's is its only secret field.
func Fields(typ string, fs []dnsprovider.Field, token string) (map[string]string, error) {
	switch typ {
	case "godaddy":
		key, secret, ok := strings.Cut(token, ":")
		if !ok || key == "" || secret == "" {
			return nil, errGoDaddyShape
		}

		return map[string]string{godaddy.FieldKey: key, godaddy.FieldSecret: secret}, nil
	case "namecheap":
		parts := strings.Split(token, ":")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return nil, errNamecheapShape
		}

		return map[string]string{namecheap.FieldUser: parts[0], namecheap.FieldKey: parts[1], namecheap.FieldClientIP: parts[2]}, nil
	}

	var secret []string

	for _, f := range fs {
		if f.Secret {
			secret = append(secret, f.Name)
		}
	}

	if len(secret) != 1 {
		return nil, fmt.Errorf("%s: dnscli cannot read one API token into its fields", typ)
	}

	return map[string]string{secret[0]: token}, nil
}
