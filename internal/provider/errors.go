package provider

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Typed Provider errors. An adapter's errors match these with errors.Is.
var (
	// ErrRateLimited means the Provider throttled the Account; see
	// APIError.RetryAfter for how long it asked to wait.
	ErrRateLimited = errors.New("rate limited")
	// ErrUnauthorized means the API token is invalid or lacks a permission.
	ErrUnauthorized = errors.New("not authorized")
	// ErrNotFound means the Provider does not know the Zone or Record.
	ErrNotFound = errors.New("not found")
	// ErrNotImplemented means the adapter does not support the operation yet.
	ErrNotImplemented = errors.New("not implemented yet")
)

// APIError is an error response from a Provider's API.
type APIError struct {
	// Provider is the Provider type, e.g. "cloudflare".
	Provider string
	// StatusCode is the HTTP status; zero for an error the Provider reported
	// inside a successful answer, which the message then leaves out.
	StatusCode int
	// Messages are the Provider's own error messages.
	Messages []string
	// RetryAfter is how long the Provider asked to wait; zero when it did not say.
	RetryAfter time.Duration
	// Kind is the typed Provider error the adapter read from the Provider's
	// own error codes, such as ErrUnauthorized. Nil classifies the error by
	// its HTTP status alone.
	Kind error
}

func (e *APIError) Error() string {
	var b strings.Builder

	var head []string

	if kind := e.kind(); kind != nil {
		head = append(head, kind.Error())
	}

	if e.StatusCode != 0 {
		head = append(head, fmt.Sprintf("(HTTP %d %s)", e.StatusCode, http.StatusText(e.StatusCode)))
	}

	b.WriteString(e.Provider)

	if len(head) > 0 {
		fmt.Fprintf(&b, ": %s", strings.Join(head, " "))
	}

	if len(e.Messages) > 0 {
		fmt.Fprintf(&b, ": %s", strings.Join(e.Messages, "; "))
	}

	if e.RetryAfter > 0 {
		fmt.Fprintf(&b, ", retry after %v", e.RetryAfter)
	}

	return b.String()
}

// Unwrap makes the error match its typed Provider error, if any.
func (e *APIError) Unwrap() error {
	return e.kind()
}

func (e *APIError) kind() error {
	if e.Kind != nil {
		return e.Kind
	}

	switch e.StatusCode {
	case http.StatusTooManyRequests:
		return ErrRateLimited
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return nil
	}
}
