# 09: Edit Records via Changeset: write gate, form, preview, Apply

**What to build:** On a Validated Zone in an unsealed session, the user can add, edit and delete Records. The edits collect into a pending Changeset, and pending changes are marked in the Records pane. The user previews the Changeset, then applies it or discards it. Apply sends the Changeset to the Provider, and the cache and Records pane update straight from the Provider's response.

On a Stale Zone or in a sealed session, the add, edit and delete actions are refused with a clear reason. This is enforced in the model's action handling, not only by hiding keys.

**Blocked by:** 07

**Status:** resolved

- [x] The write gate refuses add, edit and delete unless the Zone is validated and the session is unsealed, and the UI shows the reason
- [x] The Record form has type, name, value and TTL. Editing prefills the current values
- [x] Input is validated: the value format matches the type, the TTL is in range, and the name is inside the Zone
- [x] Add, edit and delete build a pending Changeset for the Zone, and pending changes are marked in the Records pane
- [x] The preview shows every addition, modification and deletion. The Changeset can be discarded
- [x] Apply sends the Changeset through the Provider interface, and the cache updates from the Provider's response without waiting for the SOA serial
- [x] Seam 1 tests cover the gate (stale and sealed), building, previewing, discarding, and an Apply that succeeds, asserting on the Changeset the fake Provider received and on the Store contents

## Comments

- 2026-10-07: Implemented in d853908..6238f8f. Review fixes are in the batch ending cc1b3e0.
  - Keys, Records pane only: `i` add, `e` edit, `d` delete or undo the row's pending change, `p` preview (Enter applies, Esc closes), `D` discard (inside the preview).
  - Changeset shape: `dns.Changeset{Zone, Changes []dns.Change{Kind, Before, After}}`. Modify and Delete carry the original cached `Before`, which 10 re-checks.
  - Pending Changesets live in memory per Zone. Templates (12) can append through `stageAdd`, `stageEdit` and `stageDelete`.
  - Write gate is validated AND unsealed, enforced in action handling; it also refuses while an Apply is in flight.
  - After an Apply, `Store.SaveApply` writes what the Provider confirmed, identity-checked, and the pane updates in place. Any cache failure, or a confirmed Record with no Provider ID, drops validated and asks for a Refresh.
  - TTL limits are per Provider through `provider.TTLLimits`: Cloudflare 60..86400, default 30..86400, and 1 = auto is always allowed. The proxy flag is dropped when a type change makes it invalid.
  - Partial failure is a one-line notice for now. Per-change reporting, the re-check, conflicts and the serial update are ticket 10.
  - The help overlay (21 rows) is cut off on an 80x24 terminal.
