# 10: Safe Apply: conflict re-check, partial failure, serial update

**What to build:** Before sending a Changeset, Apply re-reads the affected Records from the Provider. If they changed since the Changeset was built, Apply stops with a clear conflict message and Refreshes the Zone. If the Provider applies only some of the changes, the user sees which changes succeeded and which failed, and the cache reflects only what the Provider confirmed. When the Provider reports a new SOA serial, it is stored.

**Blocked by:** 09

**Status:** resolved

- [x] Apply re-checks the affected Records against the Provider before sending
- [x] On a conflict, nothing is sent, a conflict message is shown and the Zone is Refreshed
- [x] On a partial failure, the outcome of each change is shown, and the cache updates only for the changes the Provider confirmed
- [x] When the Provider returns a new SOA serial, it is stored
- [x] Seam 1 tests cover a conflict, a partial failure and the serial update

## Comments

- 2026-10-07: Implemented in ae2c75b..832120e and merged in 844f29f. Review fixes end at 4991667.
  - Re-check: before sending, Apply re-lists the Zone and compares each Modify/Delete `Before` with the Provider's copy. It is skipped when every change is an add.
  - Conflict: nothing is sent, each conflict is listed, and the Zone is Refreshed. `O` applies over the current values: conflicting changes are rebased onto the refreshed Records, a Delete of a vanished Record is dropped, and a Modify of one becomes an Add.
  - Partial failure: the preview shows ✓ or ✗ per change with its reason, and only the failed changes stay pending.
  - SOA serial (user requirement): the Provider's serial if it reports one; otherwise `Resolver.SOAAt` queries the Zone's own nameservers non-recursively and accepts only an authoritative answer.
    - On Refresh it runs alongside the Records listing. A failed query is not repeated for 5 minutes.
    - After an Apply, the nameservers are polled at 5s, 15s and 45s, and the first serial newer than the pre-Apply one (RFC 1982) is stored. Meanwhile the status bar shows "serial updating after Apply".
  - The background Serial Check still uses the system resolver.
