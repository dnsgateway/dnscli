# 07: Refresh on select → Validated Zone

**What to build:** When the user selects a Zone, dnscli Refreshes it from its Provider in the background. Meanwhile the cached Records stay visible and the Zone shows a refreshing indicator. A successful Refresh replaces the cached Records in one transaction, stores the SOA serial and marks the Zone as a Validated Zone for this session. A failed Refresh keeps the cached Records, leaves the Zone stale and shows the error. The user can retry a Refresh manually. Two Refreshes of the same Zone never run at the same time.

**Blocked by:** 04, 06

**Status:** resolved

- [x] Selecting a Zone starts a background Refresh. If the session is sealed, it first prompts to Unseal
- [x] The cached Records stay visible during the Refresh, and the Zone shows a refreshing indicator
- [x] A successful Refresh replaces the Zone's cached Records in one transaction and updates the SOA serial and the last Refresh time
- [x] "Validated" is held only in memory for the session and is never persisted, so a restart starts every Zone as not validated
- [x] A failed Refresh keeps the cached Records, marks the Zone stale and shows the error
- [x] A manual retry action Refreshes the Zone again
- [x] Refreshes of the same Zone are single-flight
- [x] Refresh requests go through the Provider's rate limiting
- [x] Seam 1 tests cover success, failure with the cache kept, retry, single-flight, and the restart → not validated case

## Comments

- 2026-10-07: Implemented in 6aa9187..49d580e. Merged with 05 in f82f4f0. Review fixes end at ab6d92b.
  - The predicate for 09 is `(*Model).validatedZone(id)`. Combine it with `!m.sealed()`.
  - `r` retries a Refresh. Before a rate limit's retry time, opening the Zone doesn't Refresh it and `r` only shows a notice.
  - Markers: `↻` refreshing, `✓` validated, `✗` failed.
  - Concurrency is capped by `Deps.Refreshes` (default 4), through the `zoneQueue` it shares with Serial Checks.
  - `SaveRefresh` matches the whole Zone (ID, Account, name, Provider zone ID), so a reused ID is refused. A Provider serial of 0 keeps the cached one.
  - A successful Refresh updates that one Zone in place.
  - Re-opening a Validated Zone Refreshes it again.
