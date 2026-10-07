# 13: Zone Status bar

**What to build:** A status bar shows the Zone Status of the selected Zone: whether mail records are present, DNSSEC and HSTS. Zone Status is read during a Refresh and cached, so the last known status shows even offline. For Cloudflare, DNSSEC and HSTS come from the zone settings.

**Blocked by:** 07, 08

**Status:** resolved

- [x] The status bar shows mail records present, DNSSEC and HSTS for the selected Zone
- [x] Zone Status is read during a Refresh, cached in the Store, and shown from the cache before any Refresh
- [x] The Cloudflare adapter reads DNSSEC and HSTS from the zone settings
- [x] Seam 1 tests cover the status bar from the cache and after a Refresh. Seam 2 tests cover the Cloudflare settings

## Comments

- 2026-10-07: Implemented in 6cbd66a..79b353e. Review fixes are in the batch ending ff34cb2.
  - The Zone Status has its own row above the status line. It comes from `zoneStatusItems`.
  - Mail is detected from cached Records, with MX and SPF counted only at the apex. A null MX shows "mail: none (null MX)".
  - DNSSEC and HSTS are cached by schema v3.
  - The Zone Status is read alongside the Records, with a 10s timeout. A failed or rate-limited read keeps the cached status, and a 429 sets `retryAt`.
  - Cloudflare reads DNSSEC and HSTS concurrently, and a 403 shows as unknown. `pending` maps to `dns.Pending`.
  - A changed Provider zone ID clears the cached status and serial.
