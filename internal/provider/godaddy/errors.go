package godaddy

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/sergelogvinov/dnscli/internal/provider"
)

// wireError is a v1 error body.
type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fields  []struct {
		Message string `json:"message"`
		Path    string `json:"path"`
	} `json:"fields"`
	// RetryAfterSec is how long a 429 asks to wait.
	RetryAfterSec int `json:"retryAfterSec"`
}

// apiError turns an error response into a *provider.APIError, classified
// by its HTTP status. A rate limit is reported, never waited out: the
// caller decides when to try again.
func apiError(res *http.Response, data []byte) error {
	e := &provider.APIError{Provider: "godaddy", StatusCode: res.StatusCode}

	if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 {
		e.RetryAfter = time.Duration(s) * time.Second
	}

	var body wireError
	if json.Unmarshal(data, &body) != nil {
		return e
	}

	if body.Message != "" {
		e.Messages = append(e.Messages, body.Message+" ("+body.Code+")")
	}

	for _, f := range body.Fields {
		e.Messages = append(e.Messages, f.Path+": "+f.Message)
	}

	if body.RetryAfterSec > 0 {
		e.RetryAfter = time.Duration(body.RetryAfterSec) * time.Second
	}

	return e
}
