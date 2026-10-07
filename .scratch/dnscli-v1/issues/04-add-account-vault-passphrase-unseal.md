# 04: Add an Account: encrypted secret, passphrase Unseal, Zone discovery

**What to build:** The user adds an Account by choosing a Provider, a name and an API token. The token is stored encrypted inside the database (ADR-0005). After the Account is added, dnscli discovers its Zones from the Provider and caches them.

Secrets are protected by a data key. Each enrolled Unseal Method wraps that key in its own slot. This ticket ships the passphrase method, which is chosen by flag or environment variable. Unseal happens once per session and only at the first action that needs a secret, so browsing never prompts. A sealed session is clearly shown as read-only.

This ticket introduces the Changeset-based Provider interface (ADR-0003), a scriptable in-memory fake Provider for Seam 1, and the Provider registry that maps a Provider type to its constructor.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] The Provider interface exists. It covers listing Zones, listing Records (with SOA serial when available), applying a Changeset with per-change results, reading Zone Status, and reporting the Account's expected nameservers
- [ ] A fake Provider can be scripted with Zones, Records and failures, and records the calls made to it
- [ ] The add-Account dialog takes a Provider type, a name and an API token
- [ ] The API token is stored only encrypted in the database. Account names, Provider types, Zones and Records stay in plaintext
- [ ] A data key encrypts the secrets, and the passphrase Unseal Method wraps the data key in its own slot using a memory-hard KDF
- [ ] The Unseal Method for the session can be chosen by flag or environment variable. Passphrase is the default outside macOS
- [ ] One Unseal per session unlocks every Account. No prompt appears until an action needs a secret
- [ ] The UI shows when the session is sealed. A wrong passphrase is reported, and the session stays sealed
- [ ] After an Account is added, its Zones are discovered and cached in the background and appear in the Domains pane
- [ ] When the first Account is added and when a passphrase is first enrolled, the user is warned that the database is not disposable and that losing every Unseal Method makes the secrets unrecoverable
- [ ] Seam 1 tests cover add Account → Unseal → discovery, a wrong passphrase, the deferred Unseal, and checking the stored secret is not plaintext
