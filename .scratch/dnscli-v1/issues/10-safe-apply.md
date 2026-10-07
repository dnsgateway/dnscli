# 10: Safe Apply: conflict re-check, partial failure, serial update

**What to build:** Before sending a Changeset, Apply re-reads the affected Records from the Provider. If they changed since the Changeset was built, Apply stops with a clear conflict message and Refreshes the Zone. If the Provider applies only some of the changes, the user sees which changes succeeded and which failed, and the cache reflects only what the Provider confirmed. When the Provider reports a new SOA serial, it is stored.

**Blocked by:** 09

**Status:** ready-for-agent

- [ ] Apply re-checks the affected Records against the Provider before sending
- [ ] On a conflict, nothing is sent, a conflict message is shown and the Zone is Refreshed
- [ ] On a partial failure, the outcome of each change is shown, and the cache updates only for the changes the Provider confirmed
- [ ] When the Provider returns a new SOA serial, it is stored
- [ ] Seam 1 tests cover a conflict, a partial failure and the serial update
