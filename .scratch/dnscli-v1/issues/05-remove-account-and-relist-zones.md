# 05: Remove an Account; re-list Zones

**What to build:** The user can remove an Account, which deletes its cached Zones, its Records and its encrypted secret. The user can also re-list an Account's Zones from its Provider on demand, so Zones added or removed in the Provider's console show up in dnscli.

**Blocked by:** 04

**Status:** resolved

- [x] Removing an Account asks for confirmation, then deletes the Account, its Zones, its Records and its secret
- [x] Re-listing an Account's Zones adds new Zones and drops Zones the Provider no longer has
- [x] Re-listing needs Unseal, and a Provider error leaves the cache untouched and is shown in the UI
- [x] Seam 1 tests cover removal and re-listing with added and removed Zones

## Comments

- 2026-10-07: Implemented in 4741c72..3948f69. Merged with 07 in f82f4f0. Review fixes end at ab6d92b.
  - Keys: `X` removes the highlighted Account; the confirmation asks you to type its label. `L` re-lists its Zones.
  - Removal also needs Unseal, because a sealed session is read-only.
  - Key slots are never removed, so the data key stays enrolled after the last Account is gone.
  - A re-list that returns no Zones keeps the cache and hints at token permissions.
  - A re-list or discovery whose Account was removed meanwhile is ignored.
  - Removal relies on ON DELETE CASCADE. `secure_delete` is not enabled.
