# 03: Account filter and Account labels on duplicate zone names

**What to build:** Selecting an Account in the Accounts pane limits the Domains pane to that Account's Zones. Clearing the selection shows the Zones of every Account together. When a zone name exists in more than one Account, each of those Zone entries shows its Account so the user can tell them apart.

**Blocked by:** 01

**Status:** ready-for-agent

- [x] Selecting an Account filters the Domains pane to that Account's Zones
- [x] Clearing the Account filter shows the Zones of every Account
- [x] A Zone whose zone name exists in several Accounts is shown with its Account
- [ ] The filter combines correctly with the Domains search from 02, if 02 has already landed
- [x] Seam 1 tests cover filtering, clearing and the duplicate zone name labels

## Comments

- 2026-10-07: Implemented on `ticket-03`. Enter (ActionSelect) on an Account sets the filter, and the new ActionAllAccounts (default key `*`, "Zones of all Accounts") clears it. The selected Account is marked with ✓. Duplicate zone names are labelled `<zone> · <provider>.<account>` (computed over every Account, so the label stays under a filter). The Domains pane's Zones come from one `filterZones(zones, zoneFilter)`; search from 02 adds its query as a `zoneFilter` field at merge, which meets the "combines with search" criterion. At the default 120-column width the Domains pane is 28 columns, so long labels are cut with …
