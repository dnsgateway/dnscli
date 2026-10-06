# Accounts and secrets live encrypted in SQLite

All state, including Account definitions and their secrets, lives in one pure-Go SQLite database (`modernc.org/sqlite`). API secrets are encrypted inside the database with a data key; that key is wrapped separately by each enrolled Unseal Method, like LUKS key slots, so several methods (macOS Keychain with Touch ID by default, a passphrase for recovery, others later) can coexist. The method used for a session is chosen by environment and flags. Account names, provider types, Zones and Records stay in plaintext so the cached view renders before Unseal. This keeps a single Unseal regardless of how many Accounts exist and keeps the database self-contained.

## Consequences

- The database is not a disposable cache: losing it loses Account definitions, and losing every enrolled Unseal Method makes the stored secrets unrecoverable.
