# 11: Cloudflare Changeset apply and proxy flag

**What to build:** A user can edit Records in a Cloudflare Zone end to end, including toggling the proxy flag. The Cloudflare adapter applies a Changeset as per-record create, update and delete calls and reports a result for each change.

**Blocked by:** 08, 09

**Status:** resolved

- [x] The Cloudflare adapter applies a Changeset as per-record create, update and delete calls, and returns the result of each change and the resulting Records
- [x] Any 2xx response that Cloudflare marks as success counts as success, not only HTTP 200
- [x] The proxy flag round-trips through create and update
- [x] The Record form shows the proxy flag for Cloudflare Zones
- [x] Seam 2 tests cover create, update, delete, partial failure, a non-200 2xx response and the proxy flag

## Comments

- 2026-10-07: Implemented up to 47ff6d0. Review fixes end at 4991667.
  - Mapping: POST, PATCH (so Cloudflare comments and tags survive), DELETE. Order: deletes, then modifies, then adds; results come back in Changeset order.
  - Values: MX, SRV and URI priority and CAA/SRV/URI `data` round-trip exactly with the read side.
  - The BIND export is gone: `RecordSet.Serial` is 0 and the serial comes from `SOAAt` (see 10).
  - Retries: creates are never retried. PATCH and DELETE are, and a DELETE whose retry gets a 404 counts as success.
  - A 2xx fails a change only on an explicit `"success": false`.
  - Proxy: the optional `provider.ProxyFlag` interface (Cloudflare: A, AAAA, CNAME). The form shows Proxied only for proxiable types and records. A missing flag equals "false", and the preview shows the flag.
