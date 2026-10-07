# 11: Cloudflare Changeset apply and proxy flag

**What to build:** A user can edit Records in a Cloudflare Zone end to end, including toggling the proxy flag. The Cloudflare adapter applies a Changeset as per-record create, update and delete calls and reports a result for each change.

**Blocked by:** 08, 09

**Status:** ready-for-agent

- [ ] The Cloudflare adapter applies a Changeset as per-record create, update and delete calls, and returns the result of each change and the resulting Records
- [ ] Any 2xx response that Cloudflare marks as success counts as success, not only HTTP 200
- [ ] The proxy flag round-trips through create and update
- [ ] The Record form shows the proxy flag for Cloudflare Zones
- [ ] Seam 2 tests cover create, update, delete, partial failure, a non-200 2xx response and the proxy flag
