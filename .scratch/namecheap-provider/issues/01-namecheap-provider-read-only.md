# 01: Namecheap Provider: read-only

**What to build:** A user can add a Namecheap Account and see its Zones and Records in dnscli, like a Cloudflare or GoDaddy Account. `namecheap` is in the Provider registry. The Account's API token is `APIUSER:APIKEY:CLIENTIP` in the existing token field. Its Zones are the domains on Namecheap's own DNS. They are discovered when the Account is added, can be re-listed, and can be Refreshed into Validated Zones. The Live Zone marker works against Namecheap's nameservers. Writing is not part of this ticket: until ticket 02, Apply refuses every Changeset with a clear "not supported yet" error and writes nothing. Covers user stories 1–21 and 32–38 of the spec.

**Blocked by:** None (can start immediately)

**Status:** resolved

- [x] `namecheap` is registered next to `cloudflare` and `godaddy`; an Account is labelled `namecheap.<name>`. No change to the Provider interface, the domain types, the Store schema or the TUI
- [x] The adapter talks to the Namecheap XML API over plain `net/http` and `encoding/xml`, with a `Config` that takes an optional base URL (tests, sandbox) and HTTP client; the default client has a per-request timeout
- [x] The constructor refuses a token without exactly three non-empty parts, or whose CLIENTIP is not an IPv4 address, with an error that shows the expected shape
- [x] Every request carries `ApiUser` and `UserName` (both the API user), `ApiKey`, `ClientIp` and `Command`; no request goes to a third party to discover the public IP
- [x] Zones are read from `namecheap.domains.getList` page by page (page size 100) until every domain is read, keeping only domains whose `IsOurDNS` is true. ProviderID is the domain name; a Zone's Nameservers stay nil
- [x] `Nameservers` returns `dns1.registrar-servers.com` and `dns2.registrar-servers.com`, sorted
- [x] A zone name splits into SLD (first label) and TLD (the rest), e.g. `example.co.uk` → `example` + `co.uk`
- [x] Records come from `namecheap.domains.dns.getHosts`: names become absolute (`@` is the apex), MX puts `MXPref` first in the value, TTL is taken as given (1799, Namecheap's "Automatic", stays 1799), ProviderID is the `HostId`, and URL, URL301, FRAME, ALIAS and MXE hosts are kept with their Namecheap type name. RecordSet.Serial is 0
- [x] TTLRange is 60–60000; no AutoTTL, no ProxyFlag
- [x] ZoneStatus is DNSSEC and HSTS Unknown with no error
- [x] `Status="ERROR"` responses (sent with HTTP 200) become a Provider APIError whose messages are `Number: text`. Auth errors (bad key or user, API access disabled, IP not whitelisted) map to ErrUnauthorized, unknown domain to ErrNotFound, too many requests to ErrRateLimited. Non-2xx statuses use the existing status-based classification. Rate limits are reported, never waited out; RetryAfter stays zero
- [x] Apply returns an error for every change saying Namecheap writes are not supported yet, and sends nothing
- [x] Seam 2 tests against an `httptest` server cover: malformed tokens, the auth parameters on every request, getList paging and the `IsOurDNS` filter, Nameservers, SLD/TLD splitting for a two-label and a multi-label TLD, record decoding (absolute names, apex, MX priority, HostId, Namecheap-only types), error-number mapping with number and text in the message, and ZoneStatus

## Comments

- 2026-10-07: Implemented test-first (Seam 2, `internal/provider/namecheap`), registered in `cmd/dnscli/main.go`.
  - Every command is a form-encoded POST to the endpoint, not a GET. The API key stays out of the URL, so it can't leak through a `net/http` error that quotes the URL (a test checks this). Namecheap accepts POST for every command. `Config.BaseURL` is the full endpoint (`…/xml.response`), so the sandbox URL works as-is.
  - Record values follow the other adapters: CNAME, NS, ALIAS and MX targets lose their trailing dot. Ticket 02 must not send these Records back as the "untouched" hosts. It should resend the raw getHosts hosts, as the spec says.
  - getList paging stops at a short page, or at the page that reaches `TotalItems`.
  - Error numbers: unauthorized is 1010101, 1010102, 1011100, 1011102, 1011150, 1017101, 1017105, 1017150, 1017411, 1050900; not found is 2019166, 2016166; rate limited is 500000. They come from Namecheap's global error list and third-party clients. Confirm them in the manual pass. The first number with a known kind sets the kind. An `ERROR` answer is an APIError with StatusCode 200, so its message reads `(HTTP 200 OK)`. A non-2xx answer carries no messages: Namecheap's error pages are not XML.
  - SRV and any other type come back with Namecheap's Address as-is, with no MXPref in front. Check SRV's shape in the manual pass.
  - Apply fails every change with `namecheap: writing records is not implemented yet` (wraps `provider.ErrNotImplemented`). It returns no top-level error and sends nothing.
- 2026-10-07: Review fixes (/code-review of d16bc65):
  - getList paging no longer stops early when `Paging` is missing. It stops at an empty page, at a page shorter than the `PageSize` Namecheap reports (or the 100 asked for, if it reports none), or once `TotalItems` domains are read (only when `TotalItems` > 0). A list that answers the same page twice fails instead of looping, as GoDaddy's does.
  - A bare `.` target stays `.`: a null MX reads `0 .`, and a CNAME, NS or ALIAS of `.` reads `.`.
  - SRV reads as `priority weight port target`, with the priority from `MXPref` (as for MX) and the target's trailing dot handled like other targets. An SRV address not shaped `weight port target` is kept as given. This shape comes from third-party clients; ticket 03 confirms it.
  - getHosts error 2030288 ("domain is not using proper DNS servers") is ErrNotFound, so a Zone moved off Namecheap DNS reads as not found.
  - An error number the adapter doesn't know is classified by its text: "too many requests" or "rate limit" is ErrRateLimited; request/client IP, whitelist, API key, API user, API access or "access denied" is ErrUnauthorized. This stays until the manual pass confirms the numbers.
  - `ERROR` answers no longer print `(HTTP 200 OK)`. `provider.APIError.Error` leaves out the HTTP part when StatusCode is 0, and Namecheap's in-band errors have StatusCode 0 (e.g. `namecheap: not found: 2019166: Domain not found`). Cloudflare and GoDaddy messages are unchanged.
  - Answers are read through a 4 MiB limit; a larger one is an error. A non-2xx body is not read.
  - The relative-to-absolute name helper is now shared as `provider.AbsoluteName` (with GoDaddy's `.@` handling) and used by both GoDaddy and Namecheap.
  - Known limitation, not fixed: `Nameservers` always returns the BasicDNS `dns1/dns2.registrar-servers.com`. A domain on PremiumDNS or FreeDNS may be delegated to other nameservers, so its Live Zone marker would read as not live. Fixing it needs a per-domain `namecheap.domains.getInfo` call, which the spec leaves out of scope. Ticket 03 checks it.
- 2026-10-07: Deviation from the criteria above, made in ticket 02's review: a Record's ProviderID is no longer its `HostId`. It is the Record's content (type, lower-cased name, TTL, value), as GoDaddy's is. setHosts gives every host a new HostId on every write, so cached HostIds went stale after any Apply, even for untouched Records. The next Safe Apply then reported false conflicts, and "apply over" dropped deletions and turned edits into duplicate additions. The spec's Records bullet is amended to match. `wireHost` no longer reads HostId.
