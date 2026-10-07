# 01: Namecheap Provider: read-only

**What to build:** A user can add a Namecheap Account and see its Zones and Records in dnscli, like a Cloudflare or GoDaddy Account. `namecheap` is in the Provider registry. The Account's API token is `APIUSER:APIKEY:CLIENTIP` in the existing token field. Its Zones are the domains on Namecheap's own DNS. They are discovered when the Account is added, can be re-listed, and can be Refreshed into Validated Zones. The Live Zone marker works against Namecheap's nameservers. Writing is not part of this ticket: until ticket 02, Apply refuses every Changeset with a clear "not supported yet" error and writes nothing. Covers user stories 1–21 and 32–38 of the spec.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `namecheap` is registered next to `cloudflare` and `godaddy`; an Account is labelled `namecheap.<name>`. No change to the Provider interface, the domain types, the Store schema or the TUI
- [ ] The adapter talks to the Namecheap XML API over plain `net/http` and `encoding/xml`, with a `Config` that takes an optional base URL (tests, sandbox) and HTTP client; the default client has a per-request timeout
- [ ] The constructor refuses a token without exactly three non-empty parts, or whose CLIENTIP is not an IPv4 address, with an error that shows the expected shape
- [ ] Every request carries `ApiUser` and `UserName` (both the API user), `ApiKey`, `ClientIp` and `Command`; no request goes to a third party to discover the public IP
- [ ] Zones are read from `namecheap.domains.getList` page by page (page size 100) until every domain is read, keeping only domains whose `IsOurDNS` is true. ProviderID is the domain name; a Zone's Nameservers stay nil
- [ ] `Nameservers` returns `dns1.registrar-servers.com` and `dns2.registrar-servers.com`, sorted
- [ ] A zone name splits into SLD (first label) and TLD (the rest), e.g. `example.co.uk` → `example` + `co.uk`
- [ ] Records come from `namecheap.domains.dns.getHosts`: names become absolute (`@` is the apex), MX puts `MXPref` first in the value, TTL is taken as given (1799, Namecheap's "Automatic", stays 1799), ProviderID is the `HostId`, and URL, URL301, FRAME, ALIAS and MXE hosts are kept with their Namecheap type name. RecordSet.Serial is 0
- [ ] TTLRange is 60–60000; no AutoTTL, no ProxyFlag
- [ ] ZoneStatus is DNSSEC and HSTS Unknown with no error
- [ ] `Status="ERROR"` responses (sent with HTTP 200) become a Provider APIError whose messages are `Number: text`. Auth errors (bad key or user, API access disabled, IP not whitelisted) map to ErrUnauthorized, unknown domain to ErrNotFound, too many requests to ErrRateLimited. Non-2xx statuses use the existing status-based classification. Rate limits are reported, never waited out; RetryAfter stays zero
- [ ] Apply returns an error for every change saying Namecheap writes are not supported yet, and sends nothing
- [ ] Seam 2 tests against an `httptest` server cover: malformed tokens, the auth parameters on every request, getList paging and the `IsOurDNS` filter, Nameservers, SLD/TLD splitting for a two-label and a multi-label TLD, record decoding (absolute names, apex, MX priority, HostId, Namecheap-only types), error-number mapping with number and text in the message, and ZoneStatus
