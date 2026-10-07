# 07: Refresh on select → Validated Zone

**What to build:** When the user selects a Zone, dnscli Refreshes it from its Provider in the background. Meanwhile the cached Records stay visible and the Zone shows a refreshing indicator. A successful Refresh replaces the cached Records in one transaction, stores the SOA serial and marks the Zone as a Validated Zone for this session. A failed Refresh keeps the cached Records, leaves the Zone stale and shows the error. The user can retry a Refresh manually. Two Refreshes of the same Zone never run at the same time.

**Blocked by:** 04, 06

**Status:** ready-for-agent

- [ ] Selecting a Zone starts a background Refresh. If the session is sealed, it first prompts to Unseal
- [ ] The cached Records stay visible during the Refresh, and the Zone shows a refreshing indicator
- [ ] A successful Refresh replaces the Zone's cached Records in one transaction and updates the SOA serial and the last Refresh time
- [ ] "Validated" is held only in memory for the session and is never persisted, so a restart starts every Zone as not validated
- [ ] A failed Refresh keeps the cached Records, marks the Zone stale and shows the error
- [ ] A manual retry action Refreshes the Zone again
- [ ] Refreshes of the same Zone are single-flight
- [ ] Refresh requests go through the Provider's rate limiting
- [ ] Seam 1 tests cover success, failure with the cache kept, retry, single-flight, and the restart → not validated case
