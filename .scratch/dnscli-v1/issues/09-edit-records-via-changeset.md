# 09: Edit Records via Changeset: write gate, form, preview, Apply

**What to build:** On a Validated Zone in an unsealed session, the user can add, edit and delete Records. The edits collect into a pending Changeset, and pending changes are marked in the Records pane. The user previews the Changeset, then applies it or discards it. Apply sends the Changeset to the Provider, and the cache and Records pane update straight from the Provider's response.

On a Stale Zone or in a sealed session, the add, edit and delete actions are refused with a clear reason. This is enforced in the model's action handling, not only by hiding keys.

**Blocked by:** 07

**Status:** ready-for-agent

- [ ] The write gate refuses add, edit and delete unless the Zone is validated and the session is unsealed, and the UI shows the reason
- [ ] The Record form has type, name, value and TTL. Editing prefills the current values
- [ ] Input is validated: the value format matches the type, the TTL is in range, and the name is inside the Zone
- [ ] Add, edit and delete build a pending Changeset for the Zone, and pending changes are marked in the Records pane
- [ ] The preview shows every addition, modification and deletion. The Changeset can be discarded
- [ ] Apply sends the Changeset through the Provider interface, and the cache updates from the Provider's response without waiting for the SOA serial
- [ ] Seam 1 tests cover the gate (stale and sealed), building, previewing, discarding, and an Apply that succeeds, asserting on the Changeset the fake Provider received and on the Store contents
