# 08: Cloudflare Provider, read side

**What to build:** A real Cloudflare Provider, built on `cloudflare-go` and registered in `main`. A user can add a `cloudflare.<name>` Account with an API token, see its Zones discovered, and Refresh a Zone's Records from Cloudflare. This ticket also sets up Seam 2: the adapter tested against an `httptest` server.

**Blocked by:** 04

**Status:** ready-for-agent

- [ ] The Cloudflare adapter implements Zone listing, Record listing (with SOA serial when available) and the Account's expected nameservers
- [ ] Pagination is handled for both Zones and Records
- [ ] The proxy flag is read into the Record's extras
- [ ] Rate-limit and error responses come back as typed errors, and nothing is printed to the terminal
- [ ] The API base URL is configurable, so tests can point the adapter at a test server
- [ ] The adapter is registered in the Provider registry, so a Cloudflare Account can be added from the UI
- [ ] Seam 2 tests (`httptest`) cover pagination, the proxy flag, rate limits and error mapping
