package namecheap

import (
	"strings"

	"github.com/sergelogvinov/dnscli/internal/provider"
)

// errorKinds are the typed Provider errors of Namecheap's error numbers.
// Namecheap's own docs list few of them: these come from its global error
// list and third-party clients (lego, libdns, dnscontrol).
var errorKinds = map[string]error{
	// The API user or key is missing, invalid or disabled, the Account has
	// no API access, or the client IP is not whitelisted.
	"1010101": provider.ErrUnauthorized,
	"1010102": provider.ErrUnauthorized,
	"1011100": provider.ErrUnauthorized,
	"1011102": provider.ErrUnauthorized,
	"1011150": provider.ErrUnauthorized,
	"1017101": provider.ErrUnauthorized,
	"1017105": provider.ErrUnauthorized,
	"1017150": provider.ErrUnauthorized,
	"1017411": provider.ErrUnauthorized,
	"1050900": provider.ErrUnauthorized,
	// The domain is unknown, or not the Account's.
	"2019166": provider.ErrNotFound,
	"2016166": provider.ErrNotFound,
	// getHosts of a domain that is not on Namecheap's DNS ("domain is not
	// using proper DNS servers"): the Zone has moved away.
	"2030288": provider.ErrNotFound,
	// Too many requests.
	"500000": provider.ErrRateLimited,
}

// errorTexts classify an error whose number errorKinds does not know by
// its lower-cased text, until the manual pass confirms the numbers.
var errorTexts = []struct {
	text string
	kind error
}{
	{"too many requests", provider.ErrRateLimited},
	{"rate limit", provider.ErrRateLimited},
	{"request ip", provider.ErrUnauthorized},
	{"client ip", provider.ErrUnauthorized},
	{"ip is not", provider.ErrUnauthorized},
	{"whitelist", provider.ErrUnauthorized},
	{"white list", provider.ErrUnauthorized},
	{"api key", provider.ErrUnauthorized},
	{"apikey", provider.ErrUnauthorized},
	{"api user", provider.ErrUnauthorized},
	{"apiuser", provider.ErrUnauthorized},
	{"api access", provider.ErrUnauthorized},
	{"access denied", provider.ErrUnauthorized},
}

// statusError turns an ERROR answer into a *provider.APIError, whose
// messages are each error's "Number: text" and whose kind is the first
// error that has one, by its number or else by its text. Namecheap sends
// it with HTTP 200, so the APIError has no HTTP status (StatusCode 0).
// Namecheap says nothing of when to retry: a rate limit is reported, never
// waited out.
func statusError(errs []wireError) error {
	e := &provider.APIError{Provider: "namecheap"}

	for _, w := range errs {
		e.Messages = append(e.Messages, w.Number+": "+w.Text)

		if e.Kind == nil {
			e.Kind = errorKind(w)
		}
	}

	return e
}

// errorKind is the typed Provider error of one error, nil if none.
func errorKind(w wireError) error {
	if kind, ok := errorKinds[w.Number]; ok {
		return kind
	}

	text := strings.ToLower(w.Text)
	for _, t := range errorTexts {
		if strings.Contains(text, t.text) {
			return t.kind
		}
	}

	return nil
}
