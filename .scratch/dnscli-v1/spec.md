# dnscli v1: cache-first multi-account DNS manager

Status: ready-for-agent

## Problem Statement

I manage DNS for many Zones spread across several Accounts at several Providers (today mostly Cloudflare, e.g. `cloudflare.personal` and `cloudflare.work`). To look at or change a Record I have to log into each Provider's web console, find the right Account, and then find the Zone. I can't see all my Zones in one place. I can't search Records quickly. When the same zone name exists in more than one Account, I can't tell which copy actually answers on the internet.

The existing MVP is a tview prototype with hard-coded data. It shows the three-pane layout I want, but it talks to no Provider, stores nothing and is not safe to edit with. I need a terminal app that:

- starts instantly, even offline;
- shows everything I host;
- never lets me change DNS based on stale data;
- keeps my API secrets protected on disk.

## Solution

dnscli is a Bubble Tea terminal application with three panes: Domains (upper left), Accounts (lower left) and Records (right, larger). On startup it opens one local SQLite database and immediately renders the cached Accounts, Zones and Records. Data that has not been confirmed yet is shown dimmed.

In the background, dnscli runs a Serial Check for each Zone through the system DNS resolver. The Serial Check only colours freshness. A Zone becomes a Validated Zone, and so editable, only after the user selects it and it is Refreshed from its Provider's API during the current session. Stale Zones can still be browsed and searched, but not edited.

Edits are collected into a Changeset for one Zone. The user previews the Changeset and then applies it. Before sending, dnscli re-checks the affected Records against the Provider. When the Provider reports success, the cache is updated straight from that response, without waiting for the SOA serial to change.

API secrets are stored encrypted inside the database. One Unseal per session releases the key for all Accounts: Touch ID through the macOS Keychain by default, or a passphrase. A sealed session is read-only but still fully browsable.

Cloudflare is the first Provider. The Records pane shows its proxy flag, and the status bar shows Zone Status (mail records, DNSSEC, HSTS, Live Zone).

Built-in Templates such as "Google mail", "Proton mail" and "parked domain" produce Changesets in one step.

## User Stories

### Startup and cache

1. As a user, I want dnscli to render my cached Accounts, Zones and Records immediately on launch, so that I never wait on the network to start working.
2. As a user, I want dnscli to start and be fully browsable with no network connection, so that I can inspect my DNS while offline.
3. As a user, I want dnscli to start and be browsable before I Unseal, so that I can look things up without authenticating.
4. As a first-time user with an empty database, I want a clear prompt to add my first Account, so that I know what to do next.
5. As a user, I want all of dnscli's state kept in a single database file, so that I can back it up easily.
6. As a user, I want to be told clearly that the database is not a disposable cache (it holds my Account definitions and encrypted secrets), so that I don't delete it by mistake.

### Navigation and layout

7. As a user, I want a Domains pane at the upper left, an Accounts pane at the lower left and a larger Records pane on the right, so that the layout matches the MVP I already know.
8. As a user, I want Tab and Shift+Tab to move focus to the next and previous pane, so that moving around is predictable.
9. As a user, I want the focused pane to be clearly highlighted, so that I always know where my keys go.
10. As a user, I want ↑/↓ and j/k to move within a pane, so that I can use arrows or vim keys.
11. As a user, I want Enter to select or open the highlighted item, so that drilling down is consistent everywhere.
12. As a user, I want a help overlay that lists the current keys, so that I can discover actions without reading docs.
13. As a user, I want key bindings to come from a Shortcut Profile instead of being hard-coded, so that I can change them later without code changes.
14. As a user, I want to quit cleanly with background work cancelled, so that nothing is left half-done after I exit.

### Accounts and Zones

15. As a user, I want Accounts listed as `<provider>.<account>` (e.g. `cloudflare.work`), so that I can tell same-Provider Accounts apart.
16. As a user, I want to select an Account to filter the Domains pane to that Account's Zones, so that I can focus on one Account.
17. As a user, I want to clear the Account filter and see the Zones of every Account together, so that I get one overview of everything I host.
18. As a user, I want each Zone entry to show which Account it belongs to whenever its zone name exists in more than one Account, so that I never confuse two copies of the same zone name.
19. As a user, I want the Live Zone marked among Zones that share a zone name, so that I know which copy actually answers on the internet.
20. As a user, I want to add an Account by choosing a Provider, a name and an API token, so that I can onboard new credentials from inside the app.
21. As a user, I want a new Account's Zones discovered from the Provider and cached right after I add it, so that they show up without manual steps.
22. As a user, I want to remove an Account along with its cached Zones and its secret, so that old credentials don't linger.
23. As a user, I want to re-list an Account's Zones from its Provider on demand, so that Zones added or removed in the Provider's console show up.

### Search

24. As a user, I want `/` in the Domains pane to filter Zones by zone name as I type, so that I can find a Zone among hundreds.
25. As a user, I want `/` in the Records pane to filter the selected Zone's Records by name, type or value, so that I can find a specific Record quickly.
26. As a user, I want Esc to clear the search and leave search mode, so that I can get back to the full list.
27. As a user, I want search to work on Stale Zones and in a sealed session, so that offline lookups still work.

### Freshness

28. As a user, I want cached Zones shown dimmed until they are confirmed, so that I can see at a glance which data might be old.
29. As a user, I want a Serial Check to run in the background for every Zone at startup, so that I get an early freshness hint without slowing anything down.
30. As a user, I want a Zone whose Serial Check matches to look fresher, while still not being editable, so that the hint is useful without being trusted for writes.
31. As a user, I want a Zone whose Serial Check differs or fails to be clearly marked as possibly stale, so that I know to Refresh it before relying on it.
32. As a user, I want Serial Checks to use bounded concurrency, so that hundreds of Zones don't flood my resolver.
33. As a user, I want selecting a Zone to trigger a Refresh from its Provider in the background, so that it becomes a Validated Zone without an extra step.
34. As a user, I want the Records pane to stay responsive and show the cached Records during a Refresh, so that I am never blocked.
35. As a user, I want a visible "refreshing" indicator on the Zone, so that I know validation is in progress.
36. As a user, I want a failed Refresh to leave the cached Records visible and the Zone marked stale with the error shown, so that a Provider outage doesn't take my data away.
37. As a user, I want to retry a Refresh manually, so that I can recover once the Provider is reachable again.
38. As a user, I want two Refreshes of the same Zone never to run at the same time, so that the cache can't be corrupted by a race.
39. As a user, I want Refresh requests to respect each Provider's rate limits, so that my Accounts don't get throttled.

### Write gate and editing

40. As a user, I want add, edit and delete actions disabled on Stale Zones with a clear reason, so that I never change DNS based on stale data.
41. As a user, I want add, edit and delete disabled while the session is sealed, with a prompt to Unseal, so that writing always requires my credentials.
42. As a user, I want to add a Record with type, name, value, TTL and Provider-specific fields such as Cloudflare's proxy flag, so that I can create any common Record.
43. As a user, I want to edit an existing Record in a form prefilled with its current values, so that small changes are quick.
44. As a user, I want to mark Records for deletion, so that I can remove them as part of a Changeset.
45. As a user, I want my edits to collect into a pending Changeset for the Zone instead of going out one by one, so that related changes land together.
46. As a user, I want pending additions, modifications and deletions visibly marked in the Records pane, so that I can see what will change.
47. As a user, I want to preview the Changeset before applying it, so that I can confirm exactly what will be sent.
48. As a user, I want to discard a pending Changeset, so that I can back out of edits.
49. As a user, I want basic validation of Record input (type-appropriate value format, TTL range, name inside the Zone), so that obviously bad Records are caught before they reach the Provider.
50. As a user, I want Apply to re-check the affected Records against the Provider first, and to stop with a clear conflict message if they changed since my Refresh, so that I never overwrite someone else's change.
51. As a user, I want a successful Apply to update the cache and the Records pane right away from the Provider's response, so that I see the result without waiting for the SOA serial.
52. As a user, I want a partially failed Apply to tell me exactly which changes succeeded and which failed, and to leave the cache matching what the Provider confirmed, so that I can recover with confidence.
53. As a user, I want the Zone's cached SOA serial updated after an Apply when the Provider reports it, so that the next Serial Check stays meaningful.

### Templates

54. As a user, I want to apply a built-in Template (e.g. "Google mail", "Proton mail", "parked domain") to a Validated Zone, so that common setups take one step.
55. As a user, I want a Template to ask for its parameters (e.g. a DKIM key or verification token), so that the generated Records are correct for my Zone.
56. As a user, I want a Template to produce a Changeset that I preview like any other, so that nothing is applied blindly.
57. As a user, I want singleton Records such as SPF and DMARC replaced rather than duplicated when a Template is applied, so that the Zone stays valid.
58. As a user, I want the "parked domain" Template to publish Records that block email spoofing (null MX, `v=spf1 -all`, reject DMARC), so that unused domains can't be abused for mail.

### Zone Status and Delegation

59. As a user, I want a status bar showing Zone Status for the selected Zone (mail records present, DNSSEC, HSTS, Live Zone), so that I see its health at a glance.
60. As a Cloudflare user, I want HSTS and DNSSEC state read from Cloudflare zone settings, so that Zone Status reflects Provider-side configuration.
61. As a user, I want to see a Zone's Delegation (the NS at the parent zone) next to the nameservers its Account expects, so that I understand why a Zone is or isn't the Live Zone.
62. As a user, I want Delegation to be read-only in v1, so that I can't break resolution through a half-supported registrar path.

### Accounts, secrets and Unseal

63. As a user, I want API secrets stored encrypted inside the database and never in plaintext, so that a copied database file doesn't leak my tokens.
64. As a macOS user, I want to Unseal with Touch ID through the Keychain by default, so that unlocking is fast and secure.
65. As a user on any platform, I want to enroll a passphrase Unseal Method as recovery, so that I don't lose access if the Keychain entry is lost.
66. As a user, I want to Unseal once per session no matter how many Accounts I have, so that I'm not prompted again and again.
67. As a user, I want to choose the Unseal Method for a session through a flag or environment variable, so that I can use a passphrase over SSH or in a container.
68. As a user, I want Unseal deferred until the first action that needs a secret (Refresh, Apply, Account discovery), so that pure browsing never prompts me.
69. As a user, I want a clear warning that losing every enrolled Unseal Method makes my stored secrets unrecoverable, so that I keep a recovery method enrolled.

### Robustness

70. As a user, I want Provider errors shown in the UI and never as a crash or as text printed over the terminal, so that the TUI stays usable.
71. As a user, I want Provider success judged by that Provider's API semantics, not only HTTP 200, so that valid 2xx responses aren't treated as failures.
72. As a user, I want background work never to freeze key handling, so that the interface stays responsive under load.

## Implementation Decisions

- **Rewrite the TUI on Bubble Tea** (ADR-0002) and drop tview. Background work (Serial Checks, Refreshes, Changeset applies, Account discovery) runs in goroutines that never touch UI state and report back only as Bubble Tea messages. The top-level model owns all UI state.
- **Modules**:
  - **Domain types**: Account, Zone, Record, Changeset, Zone Status. Plain value types with no Provider or UI dependencies. A Record carries a Provider-assigned ID, which is opaque to the rest of the app, plus a Provider-specific extras map (for Cloudflare, the proxy flag).
  - **Provider interface** (ADR-0003), Changeset-based and owned by dnscli. Its responsibilities:
    - list Zones for an Account;
    - list Records for a Zone, returning the SOA serial when available;
    - apply a Changeset, returning per-change results, the resulting Records and the new serial if known;
    - read Zone Status;
    - report the Account's expected nameservers.
    Success semantics live inside each adapter. Rate limiting is per Provider, inside the adapter or a shared limiter it configures.
  - **Cloudflare Provider**: built on `cloudflare-go`, with per-record CRUD under the hood. Reads DNSSEC and HSTS from zone settings. Takes a configurable API base URL so it can point at a test server.
  - **Store**: one pure-Go SQLite database (`modernc.org/sqlite`, ADR-0005) holding:
    - Accounts (name, Provider type, encrypted secret);
    - Unseal Method key slots (each wrapping the data key);
    - Zones (Account, zone name, Provider zone ID, cached SOA serial, last Refresh time, cached Zone Status, Delegation snapshot);
    - Records (Provider record ID, type, name, value, TTL, extras).
    Account names, Provider types, Zones and Records stay in plaintext so the view renders before Unseal. Schema migrations are versioned and applied on open. A Refresh replaces a Zone's Records in one transaction.
  - **Vault / Unseal**: a data key encrypts every Account secret. Each enrolled Unseal Method wraps the data key in its own slot, like LUKS key slots. v1 has two methods: the macOS Keychain with Touch ID, and a passphrase (memory-hard KDF). The method for a session is chosen by flag or environment variable, defaulting to Keychain on macOS and passphrase elsewhere. The session tracks whether it is sealed. Secrets are decrypted only in memory.
  - **Freshness**: runs Serial Checks through the system resolver with bounded concurrency, and runs Refreshes through the Provider. It allows at most one Refresh per Zone at a time (single-flight). Freshness states are: unknown, checking, hint-current, hint-stale, refreshing, validated and error. Only "validated" unlocks editing, and it is held in memory per session, never persisted.
  - **Changeset builder / Templates**: turns UI edits and Template parameters into a Changeset for one Zone. Singleton Record types (SPF, DMARC) are replaced on merge. Templates are built in and code-defined, with typed parameters.
  - **TUI**: three panes, a status bar with Zone Status and freshness, a help overlay, a Record form, a Changeset preview, an Account add/remove dialog and an Unseal prompt. Keys map to actions through a Shortcut Profile. v1 ships one default profile, and the action layer is keyed by action, not by key.
- **The top-level model is built with injected dependencies**: a Provider registry (Provider type → constructor), a resolver, Unseal Methods, the Store and a clock. `main` wires the real implementations, and tests wire fakes. This is the main test seam.
- **Write gate**: every mutating action checks two conditions, Validated Zone and unsealed session. If either fails, the action is refused with a user-visible reason. The gate is enforced in the model's action handling, not only by hiding keys.
- **Apply flow** (ADR-0001):
  1. Re-read the affected Records from the Provider and compare them with the Records the Changeset was built from.
  2. If they differ, abort with a conflict and re-Refresh the Zone.
  3. Otherwise send the Changeset.
  4. Update the cache from the Provider's per-change results.
  5. Store any new serial.
  A partial failure updates the cache only for the changes the Provider confirmed.
- **Live Zone**: for each zone name, dnscli resolves the parent Delegation (NS) through the system resolver and compares it with each Account's expected nameservers. Delegation is display-only (ADR-0004).
- **Cancellation**: all background work takes a context derived from the app's root context, which is cancelled when the app quits.
- **Go toolchain**: stays on the module's current Go version unless a dependency forces a bump. dnscontrol is not a dependency (ADR-0003).

## Testing Decisions

- **Good tests check external behaviour only.** A test sends key messages and background-result messages to the model and asserts on the rendered view, on the cache contents (read back through the Store) and on what the fake Provider received. Tests do not assert on internal fields, goroutines or intermediate states.
- **Seam 1, the top-level Bubble Tea model (main seam).** The model is built with:
  - an in-memory fake Provider that can be scripted with Zones, Records, failures, conflicts and partial-apply results, and that records the Changesets it receives;
  - a fake resolver with scripted SOA and NS answers;
  - a fake Unseal Method;
  - a real SQLite Store on a temp file.
  Tests drive the model's update function directly and run the commands it returns synchronously, so tests are deterministic and don't need a real terminal. These tests cover startup from cache, navigation, search, freshness transitions, the write gate, Changeset build, preview, apply, conflict and partial failure, Templates (including singleton replacement), the Live Zone marker, the sealed/unsealed flow and Account add/remove. Store schema, encryption round-trips and the freshness single-flight rule are covered through this seam rather than with their own unit tests.
- **Seam 2, the Cloudflare Provider against an `httptest` server.** These tests cover:
  - Zone and Record listing with pagination;
  - Changeset apply mapped to per-record create, update and delete, including per-change partial failure;
  - treating non-200 2xx responses as success;
  - reading Zone Status (DNSSEC, HSTS);
  - the proxy flag round-trip;
  - rate-limit and error responses surfacing as typed errors.
- **Out of the automated suite**: the real macOS Keychain/Touch ID method and the real system resolver are verified manually.
- **Prior art**: none. The repo has no tests yet, so these establish the pattern. Use Bubble Tea's model/message testing style (call the update function, run the returned commands, inspect the view) rather than terminal snapshot tooling.

## Out of Scope

- Domain registration: renewals, WHOIS, transfers (ADR-0004).
- Changing Delegation and registrar adapters. Namecheap registrar support comes later.
- Providers other than Cloudflare, though the interface must allow them. Namecheap DNS hosting is a follow-up.
- Search across Zones or Accounts. v1 searches only the selected Zone's Records.
- User-defined Templates and user-editable Shortcut Profiles. The mapping layer exists, but v1 ships only the defaults.
- Querying the Provider's authoritative nameservers for the Serial Check (deferred in ADR-0001).
- Unseal Methods other than the macOS Keychain and a passphrase: Linux Secret Service, Windows Credential Manager, hardware keys.
- Editing Cloudflare zone settings (TLS mode, HSTS), which v1 only displays.
- A non-interactive CLI or scripting mode.
- Import or export of zone files.

## Further Notes

- `docs/draft.md` proposed treating a matching SOA serial as validation and keeping secrets in the OS keychain. Both were superseded: ADR-0001 makes the Serial Check a hint only, and ADR-0005 stores encrypted secrets in SQLite with Unseal Method key slots. This spec follows the ADRs.
- The current tview code (three-pane layout, record editor, Zone Status flags row) is the visual reference for the Bubble Tea rewrite. It is not code to keep.
- The Store is not disposable. Losing it loses Account definitions. The app should say this when an Account is first added and when a passphrase Unseal Method is first enrolled.
