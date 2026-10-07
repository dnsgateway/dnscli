# 01: Bubble Tea app shell showing the cached data

**What to build:** Replace the tview prototype with a Bubble Tea app that opens the local SQLite Store and, on launch, renders the cached Accounts, Zones and Records in three panes: Domains (upper left), Accounts (lower left) and Records (right, larger). Cached data starts out dimmed. The user moves between panes and items with predictable keys, opens a Zone to see its Records, and can show a help overlay. An empty database shows a prompt to add the first Account.

This ticket also sets up the main test seam (Seam 1 in the spec). The top-level model is built with injected dependencies (Store, clock, and placeholders for the Provider registry, resolver and Unseal Methods). Tests drive the model's update function and run the commands it returns synchronously, with a real SQLite Store on a temp file.

**Blocked by:** None (can start immediately)

**Status:** resolved

- [x] tview is removed from the module, and the app runs on Bubble Tea
- [x] The Store uses pure-Go SQLite (`modernc.org/sqlite`) with versioned migrations applied on open. The schema holds Accounts (name, Provider type), Zones (Account, zone name, Provider zone ID, cached SOA serial, last Refresh time) and Records (Provider record ID, type, name, value, TTL, extras)
- [x] On launch, the cached data renders straight from the Store with no network access, and cached Zones show as dimmed
- [x] Tab and Shift+Tab move focus to the next and previous pane, and the focused pane is visibly highlighted
- [x] ↑/↓ and j/k move within a pane, and Enter on a Zone shows its cached Records
- [x] Accounts are listed as `<provider>.<account>`
- [x] A help overlay lists the current keys
- [x] Keys map to actions through a default Shortcut Profile, and no key is hard-coded in action handling
- [x] Quitting cancels the root context, so all background work stops
- [x] An empty database shows an "add your first Account" prompt
- [x] Seam 1 test harness exists (model + temp-file Store), with tests covering startup from cache, navigation, opening a Zone and the empty state

## Comments

- 2026-10-07: Implemented on `dnscli-v1` (8aff998..2c75179). Review fixes are in 601c6bb..f7e93f4. Deviations: the `go` directive is now 1.24 (needed by Bubble Tea) and sqlite is pinned at v1.46.1; `Model.Context()` was added so tests can check cancellation; an invalid injected Shortcut Profile is replaced by the default and the status bar says so.
