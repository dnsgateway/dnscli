package godaddy

import "testing"

// SetPageSize makes the Provider ask for pages of n domains and n records
// for the rest of the test.
func SetPageSize(t testing.TB, n int) {
	t.Helper()

	domains, records := domainsPerPage, recordsPerPage
	domainsPerPage, recordsPerPage = n, n

	t.Cleanup(func() { domainsPerPage, recordsPerPage = domains, records })
}

// WithID gives a Record the Provider ID GoDaddy's Records carry.
var WithID = withID
