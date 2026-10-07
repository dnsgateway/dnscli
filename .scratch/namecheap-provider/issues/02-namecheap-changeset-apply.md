# 02: Namecheap Changeset Apply

**What to build:** A user can add, modify and delete Records in a Namecheap Zone, and apply Templates to it, through the usual Changeset preview and Safe Apply. Namecheap's `setHosts` replaces a domain's whole host list, so Apply re-reads that list, applies the Changeset to it, and writes the full result in one call. Every host and setting the user did not change survives, and a Changeset succeeds or fails as a whole. Covers user stories 22–31 of the spec.

**Blocked by:** 01

**Status:** ready-for-agent

- [x] Apply first reads the current host list and `EmailType` with getHosts. That read, not the cached Records, is what the changes are applied to
- [x] Each change's Before is matched by HostId when it has one, otherwise by (type, name, value). A Before that is not in the current list fails the whole Changeset with a conflict, and setHosts is not called
- [x] Adding or modifying a type other than A, AAAA, CNAME, MX, TXT, NS (not at the apex) and CAA fails the whole Changeset with a clear message before any write. SRV is refused until ticket 03 confirms its form shape
- [x] The full resulting list goes in a single form-encoded POST of `namecheap.domains.dns.setHosts` (`HostName1`, `RecordType1`, `Address1`, `MXPref1`, `TTL1`, …) with the unchanged `EmailType`
- [x] Hosts the Changeset does not touch are sent back exactly as read, including URL, URL301, FRAME and ALIAS hosts with their TTLs and MX prefs
- [x] Every change shares the one call's result: all succeed, or all fail with Namecheap's error. Results are in Changeset order
- [x] On success, each added or modified ChangeResult's Record is the Record as sent; the next Refresh picks up Namecheap's new HostIds
- [x] The "not supported yet" Apply from ticket 01 is gone
- [x] Seam 2 tests cover add, modify, delete and several changes at once, checking that setHosts received the full list with every untouched host (including a URL redirect) and the original EmailType; a rejected setHosts failing every change with its error; and an unsupported write type and a missing Before refused without calling setHosts

## Comments

- 2026-10-07: Implemented test-first (Seam 2, `internal/provider/namecheap/apply.go` and `apply_test.go`; the httptest fake now scripts setHosts, records what it received and can reject it).
  - Untouched hosts are resent from the raw getHosts answer: name, type, address (trailing dots kept), MXPref (also on non-MX hosts, as read) and TTL. A modified host keeps its place in the list; added hosts go at the end.
  - Added or modified Records: the name becomes relative (`@` for the apex, case kept), CNAME, NS and MX targets get a trailing dot (`.` stays `.`), MX splits `priority host` into MXPref and Address. Only MX hosts dnscli writes carry an MXPref; others leave it out. Confirm in ticket 03 that Namecheap accepts dotted targets and a missing MXPref.
  - Matching: a Before with a HostId matches only that host, and only if the host still reads with the same type, name and value; a host edited at Namecheap under the same HostId is a conflict too. A Before without a HostId matches the first unused host with the same type, name (case-insensitive) and value. Each host matches one change at most. TTL is not compared.
  - There is no typed conflict error in `provider`, so a missing Before fails every change with `namecheap: conflict, nothing was applied: <type> <name> <value> changed at Namecheap`, wrapping `provider.ErrNotFound` as GoDaddy does.
  - Type check runs before any request (not even getHosts). Writable: A, AAAA, CNAME, MX, TXT, CAA, and NS below the apex. Deleting any type, including URL redirects and SRV, is allowed. The error names every refused Record.
  - Apply never returns a top-level error: a failed getHosts, a refused type, a conflict, a malformed MX value, an `ERROR` answer to setHosts, or `IsSuccess="false"` each fail every change with the same error. An empty Changeset sends nothing.
  - On success an added or modified ChangeResult's Record is `After` with its ProviderID cleared, since setHosts gives every host a new HostId. The TUI then counts it as not cached and needs a Refresh, as the spec accepts.
  - Risk: every setHosts renumbers all hosts, so the cached HostIds of untouched Records go stale after any Apply. Until the next Refresh, Safe Apply's re-check (by ProviderID) reports those Records as conflicts. Refreshing after an Apply clears it. Fixing it without a Refresh needs a TUI or Store change, which this ticket rules out.
  - Risk (from the spec): an edit made at Namecheap between Apply's getHosts and its setHosts is lost. That gap is one round-trip.
