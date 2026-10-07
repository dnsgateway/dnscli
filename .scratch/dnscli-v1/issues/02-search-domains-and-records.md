# 02: Search in Domains and Records

**What to build:** Pressing `/` in the Domains pane filters Zones by zone name as the user types. In the Records pane, `/` filters the selected Zone's Records by name, type or value. Esc clears the filter and leaves search. Search works the same on Stale Zones and while the session is sealed.

**Blocked by:** 01

**Status:** resolved

- [x] `/` in Domains filters Zones by zone name, live as the user types
- [x] `/` in Records filters the selected Zone's Records by name, type or value
- [x] Esc clears the filter and restores the full list
- [x] Search works on Stale Zones and in a sealed session
- [x] Seam 1 tests cover both panes' filters and clearing them

## Comments

- 2026-10-07: Implemented on `ticket-02` (5805793..7ac15fa) and merged in 9c28274, where it was combined with the Account filter from 03. Review fixes are 86d12d8..c8e7911.
  - Keys: `/` is the new Search Action. While typing, keys bound to Close, Select, NextPane or PrevPane still act, Alt+key is never typed, and every other printable key goes into the query. Backspace and Ctrl+H delete.
  - Additions beyond the ticket: Enter keeps the filter and opens the match; opening a Zone clears the Records search; `/` does nothing on an empty list.
  - The "sealed session" criterion holds only because no Vault exists yet. Re-check it once 04 lands.
