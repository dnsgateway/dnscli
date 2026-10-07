# 04: Add an Account: encrypted secret, passphrase Unseal, Zone discovery

**What to build:** The user adds an Account by choosing a Provider, a name and an API token. The token is stored encrypted inside the database (ADR-0005). After the Account is added, dnscli discovers its Zones from the Provider and caches them.

Secrets are protected by a data key. Each enrolled Unseal Method wraps that key in its own slot. This ticket ships the passphrase method, which is chosen by flag or environment variable. Unseal happens once per session and only at the first action that needs a secret, so browsing never prompts. A sealed session is clearly shown as read-only.

This ticket introduces the Changeset-based Provider interface (ADR-0003), a scriptable in-memory fake Provider for Seam 1, and the Provider registry that maps a Provider type to its constructor.

**Blocked by:** 01

**Status:** resolved

- [x] The Provider interface exists. It covers listing Zones, listing Records (with SOA serial when available), applying a Changeset with per-change results, reading Zone Status, and reporting the Account's expected nameservers
- [x] A fake Provider can be scripted with Zones, Records and failures, and records the calls made to it
- [x] The add-Account dialog takes a Provider type, a name and an API token
- [x] The API token is stored only encrypted in the database. Account names, Provider types, Zones and Records stay in plaintext
- [x] A data key encrypts the secrets, and the passphrase Unseal Method wraps the data key in its own slot using a memory-hard KDF
- [x] The Unseal Method for the session can be chosen by flag or environment variable. Passphrase is the default outside macOS
- [x] One Unseal per session unlocks every Account. No prompt appears until an action needs a secret
- [x] The UI shows when the session is sealed. A wrong passphrase is reported, and the session stays sealed
- [x] After an Account is added, its Zones are discovered and cached in the background and appear in the Domains pane
- [x] When the first Account is added and when a passphrase is first enrolled, the user is warned that the database is not disposable and that losing every Unseal Method makes the secrets unrecoverable
- [x] Seam 1 tests cover add Account → Unseal → discovery, a wrong passphrase, the deferred Unseal, and checking the stored secret is not plaintext

## Comments

- 2026-10-07: Implemented on `dnscli-v1` (7788b4a..9d1ff7d). Review fixes are in 3cd9116..b3d8fc4.
  - Flags are parsed with pflag. `--db` replaces `-db`, and `--unseal <method>` chooses the method, with `DNSCLI_UNSEAL` as the fallback. A bad command line prints usage and exits 2.
  - Secrets are XChaCha20-Poly1305 under a random data key. Passphrase key slots use argon2id, and stored parameters are bounded so a tampered row can't hang Unseal.
  - Only one data key can exist: `Store.EnrollFirstKeySlot` checks and inserts inside BEGIN IMMEDIATE, and the UI re-reads the slots before it enrolls.
  - The default build has an empty Provider registry. `-tags fakeprovider` adds a demo Provider.
  - Passphrase is also the macOS default until 15 adds the Keychain.
  - There's no standalone Unseal key yet. Refresh (07) and Apply (09) will use the same `withSecrets` gate.
