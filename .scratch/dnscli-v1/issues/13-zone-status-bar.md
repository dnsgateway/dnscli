# 13: Zone Status bar

**What to build:** A status bar shows the Zone Status of the selected Zone: whether mail records are present, DNSSEC and HSTS. Zone Status is read during a Refresh and cached, so the last known status shows even offline. For Cloudflare, DNSSEC and HSTS come from the zone settings.

**Blocked by:** 07, 08

**Status:** ready-for-agent

- [ ] The status bar shows mail records present, DNSSEC and HSTS for the selected Zone
- [ ] Zone Status is read during a Refresh, cached in the Store, and shown from the cache before any Refresh
- [ ] The Cloudflare adapter reads DNSSEC and HSTS from the zone settings
- [ ] Seam 1 tests cover the status bar from the cache and after a Refresh. Seam 2 tests cover the Cloudflare settings
