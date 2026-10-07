# 05: Remove an Account; re-list Zones

**What to build:** The user can remove an Account, which deletes its cached Zones, its Records and its encrypted secret. The user can also re-list an Account's Zones from its Provider on demand, so Zones added or removed in the Provider's console show up in dnscli.

**Blocked by:** 04

**Status:** ready-for-agent

- [ ] Removing an Account asks for confirmation, then deletes the Account, its Zones, its Records and its secret
- [ ] Re-listing an Account's Zones adds new Zones and drops Zones the Provider no longer has
- [ ] Re-listing needs Unseal, and a Provider error leaves the cache untouched and is shown in the UI
- [ ] Seam 1 tests cover removal and re-listing with added and removed Zones
