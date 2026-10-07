# Namecheap Provider

Status: ready-for-agent

## Problem Statement

Some of my Zones are hosted on Namecheap's DNS (BasicDNS / PremiumDNS), usually because Namecheap is also the registrar. dnscli shows my Cloudflare and GoDaddy Accounts, but not Namecheap. To read or change a Namecheap Record I still have to log into Namecheap's web console. Those Zones are also missing from the Domains pane, so I don't have one overview of everything I host. When a zone name is hosted both at Namecheap and somewhere else, dnscli can't show which copy is the Live Zone.

## Solution

`namecheap` becomes a Provider in the registry, next to `cloudflare` and `godaddy`. I add a Namecheap Account the same way as any other Account: choose the Provider, give it a name and paste one API token. For Namecheap the token is `APIUSER:APIKEY:CLIENTIP`, where CLIENTIP is the IPv4 address I whitelisted in Namecheap's API settings. The add-Account form and the Store don't change.

The Account's Zones are its domains that use Namecheap's own DNS. Each one is discovered, cached, Refreshed and edited like a Zone at any other Provider. Edits go through the same Changeset preview and Safe Apply. Namecheap's API replaces a domain's whole host list in one call, so an applied Changeset either succeeds or fails as a whole.

## User Stories

### Adding an Account

1. As a user, I want `namecheap` listed as a Provider when I add an Account, so that I can onboard my Namecheap credentials from inside dnscli.
2. As a user, I want to enter my Namecheap API user, API key and whitelisted client IP as one `APIUSER:APIKEY:CLIENTIP` token, so that I can use the add-Account form I already know.
3. As a user, I want a malformed token (missing a part, empty part, or a client IP that is not an IPv4 address) refused when I add the Account, with a message showing the expected shape, so that I fix it right away instead of finding out on the first API call.
4. As a user, I want my Namecheap token stored sealed like every other API secret, so that my API key is protected on disk.
5. As a user, I want my Namecheap Account labelled `namecheap.<name>` (e.g. `namecheap.main`), so that I can tell it apart from other Accounts.
6. As a user, I want my Namecheap Zones discovered and cached right after I add the Account, so that they appear without extra steps.
7. As a user whose Namecheap account has not enabled API access, or whose current IP is not whitelisted, I want a clear "not authorized" error that includes Namecheap's own message, so that I know to fix that in Namecheap's settings.
8. As a user whose IP has changed, I want the "not authorized" message to make the whitelisting problem visible, so that I know to re-add the Account with the new client IP.

### Zones

9. As a user, I want every domain in my Namecheap Account that uses Namecheap DNS to appear as a Zone, so that I see everything Namecheap hosts for me.
10. As a user, I want domains that are registered at Namecheap but delegated to another DNS host left out, so that I don't get Zones whose Records Namecheap can't serve or read.
11. As a user with more than a hundred domains, I want every page of the domain list read, so that no Zone is silently missing.
12. As a user, I want a Namecheap Zone that shares its zone name with a Zone in another Account to show its Account, so that I don't confuse the two copies.
13. As a user, I want the Live Zone marker to work for Namecheap Zones, so that I can see whether Namecheap's copy is the one that answers on the internet.
14. As a user, I want to re-list a Namecheap Account's Zones, so that domains I moved onto or off Namecheap DNS show up or disappear.

### Records

15. As a user, I want to Refresh a Namecheap Zone and see its Records, so that I can browse and search them like any other Zone.
16. As a user, I want Record names shown as absolute names with `@` read as the apex, so that Namecheap Records look the same as Records from other Providers.
17. As a user, I want MX Records shown with their priority first, so that they read the same as at Cloudflare and GoDaddy.
18. As a user, I want A, AAAA, CNAME, MX, TXT, NS (below the apex), CAA and SRV Records shown, so that I see the usual DNS content.
19. As a user, I want Namecheap-only record types (URL redirect, URL301, FRAME, ALIAS, MXE) still shown, so that I see the Zone as Namecheap serves it, even though dnscli may not create such records.
20. As a user, I want a Zone whose TTL is Namecheap's "Automatic" shown with its real TTL, so that I see what the TTL is.
21. As a user, I want a Refreshed Namecheap Zone to become a Validated Zone, so that I can edit it.

### Editing and applying

22. As a user, I want to add, modify and delete Records in a Namecheap Zone through a Changeset, so that editing works like it does for my other Providers.
23. As a user, I want TTLs outside Namecheap's allowed range refused while I edit, so that I don't build a Changeset Namecheap will reject.
24. As a user, I want Templates (Google mail, Proton mail, parked domain) to work on Namecheap Zones, so that common setups take one step there too.
25. As a user, I want Safe Apply to re-read the Zone before writing, so that an edit someone made in Namecheap's console since my Refresh is reported as a conflict instead of being overwritten.
26. As a user, I want every Record I did not change to be kept when the Changeset is applied, even though Namecheap replaces the whole host list, so that applying one change never wipes my Zone.
27. As a user, I want Records of types dnscli can't write back (URL, URL301, FRAME, ALIAS) kept unchanged when I apply a Changeset that doesn't touch them, so that my redirects survive.
28. As a user, I want the Zone's mail setting (email type) kept as it is when I apply a Changeset, so that mail forwarding or Private Email set up in Namecheap's console keeps working.
29. As a user, I want a Changeset that Namecheap rejects to fail as a whole, with every change showing Namecheap's message, so that I know nothing was written.
30. As a user, I want a Changeset that Namecheap accepts to update the cache from what I sent, so that I don't wait for a new SOA serial.
31. As a user, I want a change to a Record type Namecheap doesn't support refused with a clear message, so that I'm not left guessing why it failed.

### Zone Status and Delegation

32. As a user, I want the status bar for a Namecheap Zone to show mail records and the Live Zone marker, so that I get the same overview as for other Zones.
33. As a user, I want DNSSEC and HSTS shown as unknown for Namecheap Zones, without an error, so that a missing setting doesn't look like a failure.
34. As a user, I want the nameservers Namecheap expects (`*.registrar-servers.com`) listed for the Account, so that Delegation checks can find the Live Zone.

### Rate limits and errors

35. As a user, I want Namecheap's rate limit reported as "rate limited" rather than waited out, so that dnscli never hangs.
36. As a user, I want a domain Namecheap doesn't know reported as "not found", so that I can tell it apart from a credentials problem.
37. As a user, I want any other Namecheap error to include its error number and message, so that I can look it up.
38. As a user, I want each request bounded by a timeout, so that an unresponsive Namecheap API never freezes the app.

## Implementation Decisions

- **New Provider adapter, `namecheap`, in dnscli's own provider module (ADR-0003).** It implements the Provider interface (Zones, Records, Apply, ZoneStatus, Nameservers) and TTLLimits. It does not implement AutoTTL or ProxyFlag. It is registered in `main`'s Provider registry next to `cloudflare` and `godaddy`. No change to the Provider interface, the domain types, the Store schema or the TUI.
- **Plain HTTP, no SDK.** The adapter talks to the Namecheap XML API (`https://api.namecheap.com/xml.response`) over `net/http` and decodes XML with `encoding/xml`. Like the GoDaddy adapter, it has a `Config` with an optional base URL (so tests and the Namecheap sandbox can be used) and an optional HTTP client. The default client applies a per-request timeout. Third-party Go clients exist, but they are thin and not maintained by Namecheap, so they are not used.
- **Token shape.** The token is `APIUSER:APIKEY:CLIENTIP` in the existing token field. The constructor refuses a token without exactly three non-empty parts, or whose CLIENTIP does not parse as an IPv4 address, and returns an error that shows the expected shape. Every request sends `ApiUser` and `UserName` (both the API user), `ApiKey`, `ClientIp` and `Command`. dnscli does not discover the public IP itself, so no request goes to a third party. If the IP changes, the user re-adds the Account.
- **Zones.** `namecheap.domains.getList` is read page by page, with the largest page size Namecheap serves (100), until every domain has been read. Zones are only the domains whose `IsOurDNS` is true: domains on custom nameservers have no host list Namecheap will read. Expired and locked domains stay as Namecheap lists them. ProviderID is the domain name. A Zone's Nameservers are left nil. `Nameservers` returns Namecheap's DNS nameservers (`dns1.registrar-servers.com`, `dns2.registrar-servers.com`), sorted, so the Live Zone check can compare Delegation against them. PremiumDNS / FreeDNS sets are a follow-up if the manual pass shows they matter.
- **Splitting a zone name.** Namecheap addresses a domain as `SLD` + `TLD`. The adapter splits the zone name at its first label: the SLD is the first label and the TLD is everything after it (`example.co.uk` → `example` + `co.uk`). Namecheap only registers domains at public-suffix boundaries, so this split is right for any domain the Account holds.
- **Records.** `namecheap.domains.dns.getHosts` returns the whole host list in one response; there is no paging. Each host becomes a Record: names become absolute (`@` is the apex); MX puts `MXPref` first in the value, as the other adapters do; TTL is taken as given. Each Record's ProviderID is its content: type, lower-cased name, TTL and value, as the GoDaddy adapter does. *(Changed 2026-10-07, after review of ticket 02: this spec first used Namecheap's `HostId`. But every setHosts renumbers every host, so after any Apply the HostIds dnscli cached, even for untouched Records, were stale. Safe Apply then reported false conflicts, and "apply over" dropped deletions and turned edits into duplicate additions. A content ID stays the same as long as the Record does.)* RecordSet.Serial is 0, since Namecheap does not report the SOA serial. The domain's `EmailType` is not a Record and is not stored. Namecheap-only types (URL, URL301, FRAME, ALIAS, MXE) are read and shown with their Namecheap type name.
- **Apply is a whole-zone replace.** `namecheap.domains.dns.setHosts` replaces every host of the domain. Apply first reads the current host list and the current `EmailType` with getHosts. Safe Apply has already re-read the Zone, so this second read is Apply's own source of truth. Apply then applies every change in the Changeset to that list, matching each Before by its content ProviderID when it has one, otherwise by (type, name, value). It sends the full resulting list in a single POST (form-encoded `HostName1`, `RecordType1`, `Address1`, `MXPref1`, `TTL1`, …) together with the unchanged `EmailType`. Every change shares that one call's result: all succeed, or Apply fails as a whole. Results are in Changeset order. On success, each added or modified ChangeResult's Record is the Record as sent, read back the way Records reads it, with its content ProviderID, so the TUI caches it without a Refresh. *(Changed 2026-10-07: the Record first went back without a ProviderID, since HostIds change on every setHosts; see Records.)* When the Changeset fails (refused type, a mail setting that is not Custom MX, a conflict, a failed getHosts, a rejected or failed setHosts), Apply returns an error of its own rather than one per change, so the TUI reports that nothing was applied and keeps every change pending. MX additions and modifications need the domain's `EmailType` to be MX ("Custom MX" in Namecheap's console): Namecheap ignores MX hosts otherwise, so Apply fails the Changeset after its getHosts read with a message saying so, and never changes the EmailType itself (story 28).
- **Hosts dnscli did not touch are sent back exactly as read**, including Namecheap-only types, their TTLs and MX prefs. A Change whose Before can't be found in the current list fails the whole Changeset with a conflict, and nothing is sent.
- **Supported write types**: A, AAAA, CNAME, MX, TXT, NS (not at the apex). A Changeset that adds or modifies any other type (including SRV and CAA until the manual pass confirms Namecheap's form shape for them) fails as a whole with a clear message before any request. Deleting any type is allowed. *(Changed 2026-10-07: CAA was first writable. Its setHosts encoding comes only from third-party clients, so CAA writes wait for ticket 03, like SRV.)*
- **TTLRange is 60–60000.** dnscli has no automatic TTL for Namecheap. A TTL of 1799, Namecheap's "Automatic", is read and written as 1799 seconds.
- **ZoneStatus** returns DNSSEC and HSTS as Unknown with no error. The getHosts response doesn't report either, and DNSSEC lives on the registrar side, which is out of scope (ADR-0004).
- **Errors.** Namecheap answers HTTP 200 even when the call failed. The adapter treats `Status="ERROR"` in the `ApiResponse` as a failure and builds a Provider APIError whose Messages are each error's `Number: text`. It sets APIError.Kind from Namecheap's error numbers: invalid API key / user / disabled API access / non-whitelisted IP become ErrUnauthorized; unknown domain becomes ErrNotFound; "too many requests" becomes ErrRateLimited. Non-2xx HTTP statuses fall back to the existing status-based classification. Rate limits are reported, never waited out. RetryAfter stays zero, because Namecheap doesn't give one.
- **ADR-0004 holds.** Namecheap is also a registrar, but this spec only adds DNS hosting. Changing Delegation through Namecheap's registrar API (`domains.dns.setCustom` / `setDefault`) stays a later registrar adapter.

## Testing Decisions

- **One seam: Seam 2, the Namecheap Provider against an `httptest` server** that speaks the Namecheap XML API's request parameters and response shapes. Same pattern as the GoDaddy and Cloudflare adapter tests. No Seam 1 (TUI model) tests are added: the UI is unchanged and already covered by Seam 1 with the fake Provider.
- **Good tests check external behaviour only.** They call the Provider's methods through the Provider interface. They assert on what comes back (Zones, Records, ApplyResult, typed errors through `errors.Is`) and on what the scripted API received (the commands, auth parameters and setHosts host lists). They don't assert on internal helpers or XML struct shapes.
- **What the tests cover:**
  - the constructor refusing malformed tokens (missing part, empty part, non-IPv4 client IP);
  - every request carrying ApiUser, UserName, ApiKey and ClientIp from the token;
  - Zones read across several getList pages, keeping only `IsOurDNS` domains, with the domain as ProviderID;
  - Nameservers returning Namecheap's sorted nameservers;
  - SLD/TLD splitting for a two-label and a multi-label TLD;
  - record decoding: absolute names, `@` as the apex, MX priority first, content as ProviderID, Namecheap-only types kept;
  - Apply for add, modify, delete and several changes at once, checking that setHosts received the full list with every untouched host (including URL redirects) and the original EmailType;
  - Apply failing as a whole when Namecheap returns an error, with every change carrying it;
  - Apply refusing an unsupported write type, and a missing Before, without calling setHosts;
  - error mapping from Namecheap error numbers to ErrUnauthorized, ErrNotFound and ErrRateLimited, with the number and text in the message;
  - ZoneStatus returning Unknown/Unknown with no error.
- **Prior art:** the GoDaddy adapter's tests, with a scripted fake API that records requests and writes and can be told to reject a write. The Cloudflare adapter tests are the second example.
- **Manual pass:** verify against a real Namecheap Account (or the Namecheap sandbox): token and whitelisting errors, Zones, Records including CAA and a URL redirect, an Apply that keeps untouched hosts and the email setting, and the real error numbers for auth, not-found and rate limit.

## Out of Scope

- Changing Delegation through Namecheap's registrar API, and any other registrar function (ADR-0004).
- Domains that Namecheap registers but that are delegated to another DNS host. They are not Zones of a Namecheap Account.
- Editing the domain's email type (mail forwarding, Private Email) or Namecheap's URL-redirect, URL301, FRAME and ALIAS records. They are shown and kept, not written.
- SRV and CAA writes until the manual pass confirms Namecheap's API shape for them.
- Detecting the client's public IP automatically.
- DNSSEC and other registrar-side settings in Zone Status.
- PremiumDNS and FreeDNS nameserver sets in `Nameservers` (follow-up if needed).
- Retrying or waiting out rate limits.

## Further Notes

- **Risk: API access eligibility.** Namecheap only enables API access for accounts that meet its spending or domain-count rules, and only from whitelisted IPv4 addresses. An ineligible Account, or one whose IP changed, gets an auth error on every call, which shows as "not authorized".
- **Risk: whole-zone replace.** setHosts overwrites everything. Apply re-reads the host list right before writing and sends back every host it didn't change, and Safe Apply turns a concurrent console edit into a conflict. Even so, an edit that lands in the gap between Apply's read and its write is lost. That gap is one round-trip.
- **Risk: details to confirm in the manual pass.** The exact error numbers, the "Automatic" TTL value, CAA's value encoding and whether getHosts returns apex NS records come from third-party clients (lego, libdns, dnscontrol), not Namecheap's own docs.
- **Rate limits** (published by Namecheap): about 20 calls per minute, 700 per hour and 8000 per day per user. A full re-list of a large Account is several getList pages, so it fits easily.
- This spec has no implementation tickets yet. Break it into `.scratch/namecheap-provider/issues/` when work starts. Read-only first, then Apply, if a split helps.
