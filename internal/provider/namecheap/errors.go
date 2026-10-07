package namecheap

import (
	"net/http"

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
	// Too many requests.
	"500000": provider.ErrRateLimited,
}

// statusError turns an ERROR answer into a *provider.APIError, whose
// messages are each error's "Number: text" and whose kind is the first
// error number that has one. Namecheap sends it with HTTP 200, and says
// nothing of when to retry: a rate limit is reported, never waited out.
func statusError(errs []wireError) error {
	e := &provider.APIError{Provider: "namecheap", StatusCode: http.StatusOK}

	for _, w := range errs {
		e.Messages = append(e.Messages, w.Number+": "+w.Text)

		if e.Kind == nil {
			e.Kind = errorKinds[w.Number]
		}
	}

	return e
}
