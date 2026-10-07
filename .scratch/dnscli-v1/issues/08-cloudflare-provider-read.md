# 08: Cloudflare Provider, read side

**What to build:** A real Cloudflare Provider, built on `cloudflare-go` and registered in `main`. A user can add a `cloudflare.<name>` Account with an API token, see its Zones discovered, and Refresh a Zone's Records from Cloudflare. This ticket also sets up Seam 2: the adapter tested against an `httptest` server.

**Blocked by:** 04

**Status:** resolved

- [x] The Cloudflare adapter implements Zone listing, Record listing (with SOA serial when available) and the Account's expected nameservers
- [x] Pagination is handled for both Zones and Records
- [x] The proxy flag is read into the Record's extras
- [x] Rate-limit and error responses come back as typed errors, and nothing is printed to the terminal
- [x] The API base URL is configurable, so tests can point the adapter at a test server
- [x] The adapter is registered in the Provider registry, so a Cloudflare Account can be added from the UI
- [x] Seam 2 tests (`httptest`) cover pagination, the proxy flag, rate limits and error mapping

## Comments

- 2026-10-07: Implemented on cloudflare-go/v7 v7.12.0, up to d649f3b. Review fixes are in the batch ending d6153a7.
  - The SOA serial comes from the BIND export (`/dns_records/export`). It has NOT been confirmed against live Cloudflare that this matches the serial its nameservers serve.
  - Paging and record decoding are done by hand: the SDK drops the context after page 1 and decodes every record as A.
  - MX, SRV and URI values put the priority first. The proxy flag is stored as `Extras["proxied"]`.
  - Typed errors map both HTTP status and Cloudflare error codes. A 429 is not retried and is returned with `RetryAfter`. There is no client-side throttle yet.
  - Each request times out after 30s by default (`Config.RequestTimeout`).
  - Not run end to end against real Cloudflare.
