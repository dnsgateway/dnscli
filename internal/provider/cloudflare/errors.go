package cloudflare

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/cloudflare/cloudflare-go/v7/option"

	"github.com/sergelogvinov/dnscli/internal/provider"
)

// reportRateLimits stops cloudflare-go from waiting out a rate limit: its
// Retry-After can be minutes, so the caller gets provider.ErrRateLimited
// and decides when to try again. Server errors are still retried.
func reportRateLimits(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	res, err := next(req)
	if res != nil && res.StatusCode == http.StatusTooManyRequests {
		res.Header.Set("X-Should-Retry", "false")
	}

	return res, err
}

// apiError turns a failed cloudflare-go call into a *provider.APIError
// when the API answered with an error status, given that call's last
// response. Other errors, such as a lost connection, are wrapped as is.
func apiError(err error, res *http.Response) error {
	if res == nil || res.StatusCode < http.StatusBadRequest {
		return fmt.Errorf("cloudflare: %w", err)
	}

	e := &provider.APIError{
		Provider:   "cloudflare",
		StatusCode: res.StatusCode,
		RetryAfter: retryAfter(res.Header.Get("Retry-After")),
	}

	// cloudflare-go leaves the error body readable.
	var body struct {
		Errors []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}

	if data, rerr := io.ReadAll(res.Body); rerr == nil && json.Unmarshal(data, &body) == nil {
		for _, m := range body.Errors {
			e.Messages = append(e.Messages, fmt.Sprintf("%s (code %d)", m.Message, m.Code))
		}
	}

	return e
}

// retryAfter parses a Retry-After header: delay seconds or an HTTP date.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}

	if s, err := strconv.Atoi(v); err == nil {
		return max(0, time.Duration(s)*time.Second)
	}

	if t, err := http.ParseTime(v); err == nil {
		return max(0, time.Until(t).Round(time.Second))
	}

	return 0
}
