package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dnsgateway/dnsprovider"
	"github.com/dnsgateway/dnsprovider/providers/namecheap"
)

// Credentials reads an Account's unsealed credentials into the field map
// its Provider of type typ is built from. They are sealed as a JSON field
// map; anything else is the old single API token, read into the Provider's
// fields until the Account is saved again (see EncodeCredentials).
func (o *Opener) Credentials(typ string, plain []byte) (map[string]string, error) {
	f, err := o.factory(typ)
	if err != nil {
		return nil, err
	}

	var fields map[string]string
	if json.Unmarshal(plain, &fields) == nil && fields != nil {
		return fields, nil
	}

	return oldTokenFields(typ, f.Fields, string(plain))
}

// EncodeCredentials is the plaintext an Account's field map is sealed as:
// a JSON object, which Credentials reads back.
func EncodeCredentials(fields map[string]string) []byte {
	// A map of strings always encodes.
	b, _ := json.Marshal(fields)

	return b
}

// errNamecheapShape is the shape of Namecheap's old single token, which
// holds several fields.
var errNamecheapShape = errors.New("namecheap: the API token is the API user, API key and whitelisted IPv4 client IP as APIUSER:APIKEY:CLIENTIP")

// oldTokenFields reads an old single API token into the field map its
// Provider is built from. Namecheap's token is APIUSER:APIKEY:CLIENTIP;
// any other Provider's is its only secret field.
func oldTokenFields(typ string, fs []dnsprovider.Field, token string) (map[string]string, error) {
	if typ == "namecheap" {
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
