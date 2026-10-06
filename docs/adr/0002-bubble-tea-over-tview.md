# Bubble Tea instead of tview

The MVP was written in tview, but the TUI is rebuilt on Bubble Tea. Background work (Serial Checks, refreshes, Changeset applies) runs in goroutines that never touch UI state and report back only as Bubble Tea messages, which fits the concurrent cache-first design better than tview's shared widgets plus `QueueUpdateDraw`.
