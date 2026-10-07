# 16: GoDaddy Provider

**What to build:** A user can add a GoDaddy Account, browse its Zones and Records, and edit Records end to end. The GoDaddy adapter talks to the v1 Domains API (`https://api.godaddy.com`) over plain `net/http`; there is no maintained Go SDK.

**Blocked by:** 08, 11

**Status:** ready-for-human

- [x] `godaddy` is in the Provider registry. The Account's API token is `KEY:SECRET` in the existing token field, sent as `Authorization: sso-key KEY:SECRET`; a token without a colon is refused by the constructor
- [x] Zones are the Account's ACTIVE domains (`GET /v1/domains?statuses=ACTIVE&includes=nameServers`, paged by `marker`). ProviderID is the domain name
- [x] A Zone carries its nameservers only when they are all `*.domaincontrol.com`; `Nameservers` returns every such nameserver across the Account, sorted, so a Zone delegated elsewhere falls back to them
- [x] Records are read from `GET /v1/domains/{d}/records`, paged by `offset`/`limit`. Names become absolute (`@` is the apex). MX and SRV put the priority first, as in the Cloudflare adapter. GoDaddy has no record IDs, so a Record's ProviderID is its type, lowercased name, TTL and Value (what Apply matches it by); RecordSet.Serial is 0
- [x] Apply groups a Changeset by (type, name) RRset. Each group reads the RRset (`GET /v1/domains/{d}/records/{type}/{name}`), makes its changes, and writes it with one `PUT` of the same path, or one `DELETE` when it becomes empty. Every change in a group shares its call's result. Results are in Changeset order
- [x] TTLRange is 600–86400; no AutoTTL, no ProxyFlag
- [x] ZoneStatus is DNSSEC and HSTS Unknown with no error (v1 reports neither)
- [x] 401/403, 404 and 429 map to the typed Provider errors (422 stays unclassified); 429's `retryAfterSec` becomes RetryAfter. GoDaddy's `code`/`message`/`fields` become the APIError messages. Rate limits are reported, never waited out
- [x] Seam 2 tests cover Zone and Record paging, record decoding, Apply grouping (add, modify, delete, RRset becomes empty, two changes in one RRset), partial failure, error mapping and the auth header
- [ ] Manually verified against a real GoDaddy Account

## Comments

- 2026-10-07: Decisions taken with the user: one `KEY:SECRET` field (no form or store change), all ACTIVE domains are Zones, read and Apply in one ticket.
  - Risk: GoDaddy restricts Domains API access to larger accounts. An ineligible Account gets 403 on every call, which shows as "not authorized".
  - Risk: a PUT replaces the whole RRset from dnscli's copy. Safe Apply (ticket 10) re-reads the Zone first, so a concurrent edit is a conflict, not a silent overwrite.
  - Risk: the SRV (`service`, `protocol`, `port`, `weight`) and CAA JSON shapes come from third-party clients (lego, libdns), not GoDaddy's own docs. Check them in the manual pass. URI is not a GoDaddy type; Apply fails such a change.
- 2026-10-07: Implemented test-first (Seam 2, `internal/provider/godaddy`), registered in `cmd/dnscli/main.go`. Status is ready-for-human until the manual pass below.
  - Apply reads each RRset just before writing it. A Before that is not in the RRset fails that change with ErrNotFound, and an RRset whose changes all fail is not written. A Modify that changes the name or type removes from one RRset and adds to another. RRsets left empty are deleted first.
  - An SRV RRset is named by its host part (`@` for `_sip._tcp.example.com`), with `service`/`protocol` in each record, so writing one SRV record rewrites every SRV record at that host. URI changes fail; CAA `data` passes through in presentation form.
  - Record paging takes `offset` as the page, from 1, as libdns does. GoDaddy's spec says it counts records to skip; check on a zone of 500+ records.
- 2026-10-07: Review fixes (findings 1–6 of /code-review):
  - A rename across RRsets that is made in only one of them is undone. If the undo also fails, the error says whether the old Record is gone or the new one was added.
  - A removal matches the name (ignoring case) as well as value and TTL, so a delete in a shared SRV RRset hits its own service.
  - Apply keeps the names GoDaddy returns, so an SRV record named `_sip._tcp` without service fields keeps its prefix.
  - A target of `@` reads as the zone name for MX and SRV as well as CNAME and NS.
  - Domain and record paging stop at a page shorter than asked for. A list that answers the same page twice fails instead of looping.
  - Still open from the review: `Nameservers` re-lists every domain for an almost always empty answer, the Retry-After parsing is copied from Cloudflare, and the name and priority helpers duplicate `dns.RelativeName` and Cloudflare's code.

### Manual GoDaddy verification

1. `curl -H "Authorization: sso-key KEY:SECRET" https://api.godaddy.com/v1/domains` answers 200, not 403.
2. Add Account `godaddy.<name>` with token `KEY:SECRET`. Its ACTIVE domains are listed; a domain on `*.domaincontrol.com` is the Live Zone.
3. Select a Zone: Records match the GoDaddy console, including MX priority, any SRV (`_service._proto.host`) and CAA.
4. Add an A Record next to an existing one with the same name, then delete it: the other stays.
5. Rename a Record (Modify the name): the old name is gone and the new one is there.
6. Try TTL 300: the Record form refuses it.
7. Add and delete an SRV Record; check the other SRV Records at the same host survive.
- 2026-10-07: Fix: Records came back with no ProviderID, so the TUI refused to edit or delete any GoDaddy Record ("has no Provider ID"). Records and Apply results now carry a content-derived ID. A cached Record whose ID no longer matches after an edit elsewhere is caught by Safe Apply's re-read, as before.
