# 14: Delegation and Live Zone

**What to build:** For each zone name, dnscli resolves the Delegation (the parent NS) through the system resolver and compares it with each Account's expected nameservers. The Zone that matches is marked as the Live Zone in the Domains pane. A read-only Delegation view shows the delegated nameservers next to the ones the Account expects. Per ADR-0004, Delegation cannot be changed in v1.

**Blocked by:** 04, 06

**Status:** resolved

- [x] The parent NS is resolved in the background through the injected resolver
- [x] Among Zones that share a zone name, the one whose Account's nameservers match the Delegation is marked as the Live Zone
- [x] A read-only Delegation view shows the delegated nameservers and the Account's expected nameservers
- [x] A resolver failure leaves the Live Zone unknown and does not raise an error dialog
- [x] The Live Zone is also shown in the Zone Status bar, if 13 has already landed
- [x] Seam 1 tests cover a match, a mismatch, a zone name shared by two Accounts, and a resolver failure

## Comments

- 2026-10-07: Implemented up to f681a3d. Review fixes are in the batch ending ff34cb2.
  - `Resolver.NS` returns the apex NS through the system resolver, not the parent's own NS set.
  - Expected nameservers are per Zone, from Cloudflare's zone list, cached by schema v4. A Zone listed without them takes the set its sibling Zones share, or stays unknown if they disagree.
  - Each zone name is looked up once. A result expires after 10 minutes and is looked up again on re-list. Lookups share the Serial Check concurrency limit.
  - Marker: a green ● for the Live Zone. `N` opens the read-only Delegation view. The Live Zone also shows in the Zone Status row.
  - A Zone that is not the Live Zone shows no Serial Check hint.
  - Zones cached before v4 stay unknown until their Account is re-listed.
