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
	"github.com/dnsgateway/dnsprovider/providers/cloudflare"
	"github.com/dnsgateway/dnsprovider/providers/godaddy"
	"github.com/dnsgateway/dnsprovider/providers/namecheap"
)

// fieldTenantID is the field of a Provider whose Account acts within one
// Tenant it picks, such as Cloudflare.
const fieldTenantID = cloudflare.FieldTenantID

// Open builds the Provider of type typ for an Account from its stored API
// token, through the Registry. The token is read into the Provider's
// fields, as Fields reads it.
//
// A Provider that takes a Tenant and can list them gets the only Tenant
// the token reaches; a token that reaches several or none fails, since
// dnscli stores no Tenant yet. The Tenant found is kept for the token for
// the life of the process, so it is listed once.
func Open(ctx context.Context, reg dnsprovider.Registry, typ, token string, opts ...dnsprovider.Option) (dnsprovider.Provider, error) {
	f, ok := reg[typ]
	if !ok || f.New == nil {
		return nil, fmt.Errorf("Provider type %q is not available in this build", typ)
	}

	fields, err := Fields(typ, f.Fields, token)
	if err != nil {
		return nil, err
	}

	if !slices.ContainsFunc(f.Fields, func(fd dnsprovider.Field) bool { return fd.Name == fieldTenantID }) {
		return f.New(fields, opts...)
	}

	key := tenantKey(typ, token)
	if id, ok := tenants.Load(key); ok {
		fields[fieldTenantID] = id.(string)

		return f.New(fields, opts...)
	}

	p, err := f.New(fields, opts...)
	if err != nil {
		return nil, err
	}

	lister, ok := p.(dnsprovider.TenantLister)
	if !ok {
		return p, nil
	}

	id, err := onlyTenant(ctx, typ, lister)
	if err != nil {
		return nil, err
	}

	tenants.Store(key, id)
	fields[fieldTenantID] = id

	return f.New(fields, opts...)
}

// tenants are the Tenants found for each token, by tenantKey.
var tenants sync.Map

// tenantKey keys a token's Tenant without holding the token itself.
func tenantKey(typ, token string) string {
	sum := sha256.Sum256([]byte(typ + "\x00" + token))

	return string(sum[:])
}

// onlyTenant is the one Tenant a token reaches.
func onlyTenant(ctx context.Context, typ string, l dnsprovider.TenantLister) (string, error) {
	list, err := l.Tenants(ctx)
	if err != nil {
		return "", fmt.Errorf("list the %s accounts the API token reaches: %w", typ, err)
	}

	switch len(list) {
	case 0:
		return "", fmt.Errorf("the API token reaches no %s account", typ)
	case 1:
		return list[0].ID, nil
	}

	names := make([]string, len(list))
	for i, t := range list {
		names[i] = t.Name
		if names[i] == "" {
			names[i] = t.ID
		}
	}

	return "", fmt.Errorf("the API token reaches %d %s accounts (%s); dnscli cannot pick one yet: use a token that reaches one",
		len(list), typ, strings.Join(names, ", "))
}

// Shapes of the old single-token credentials that hold several fields.
var (
	errGoDaddyShape   = errors.New("godaddy: the API token is the key and secret as KEY:SECRET")
	errNamecheapShape = errors.New("namecheap: the API token is the API user, API key and whitelisted IPv4 client IP as APIUSER:APIKEY:CLIENTIP")
)

// Fields reads an Account's stored API token into the field map its
// Provider is built from. GoDaddy's token is KEY:SECRET, Namecheap's
// APIUSER:APIKEY:CLIENTIP; any other Provider's is its "token" field, or
// its only field.
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

	if slices.ContainsFunc(fs, func(f dnsprovider.Field) bool { return f.Name == cloudflare.FieldToken }) {
		return map[string]string{cloudflare.FieldToken: token}, nil
	}

	if len(fs) == 1 {
		return map[string]string{fs[0].Name: token}, nil
	}

	return nil, fmt.Errorf("%s: dnscli cannot read one API token into its fields", typ)
}
