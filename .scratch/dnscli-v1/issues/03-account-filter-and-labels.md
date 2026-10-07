# 03: Account filter and Account labels on duplicate zone names

**What to build:** Selecting an Account in the Accounts pane limits the Domains pane to that Account's Zones. Clearing the selection shows the Zones of every Account together. When a zone name exists in more than one Account, each of those Zone entries shows its Account so the user can tell them apart.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] Selecting an Account filters the Domains pane to that Account's Zones
- [ ] Clearing the Account filter shows the Zones of every Account
- [ ] A Zone whose zone name exists in several Accounts is shown with its Account
- [ ] The filter combines correctly with the Domains search from 02, if 02 has already landed
- [ ] Seam 1 tests cover filtering, clearing and the duplicate zone name labels
