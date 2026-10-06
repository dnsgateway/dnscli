# Handoff: Multi-Provider DNS Manager CLI/TUI

You are taking over the design of a **Go-based CLI/TUI application for managing DNS records across multiple DNS providers**. Continue from the decisions below rather than redesigning the project from scratch.

## Product goal

Build a fast terminal application that allows users to manage DNS zones and records across providers such as Cloudflare, Namecheap, and others.

The application should support:

- Multiple DNS providers.
- Multiple accounts for each provider.
- Multiple zones per account.
- Local cache/database of zones and DNS records.
- Secure local storage of provider credentials.
- OS-native keychain where possible, especially Apple Keychain on macOS.
- Potential biometric authorization, such as Touch ID, when accessing credentials.
- Cache-first operation.
- Graceful operation when a provider is temporarily unreachable.
- Interactive TUI as the primary interface.
- Go implementation; Bubble Tea is a likely TUI framework.

---

## Core UX

The existing MVP uses three logical panes:

```text
┌─────────────────────────────┬───────────────────────────────────────────────┐
│ Domains                     │ Records                                       │
│                             │                                               │
│ example.com                 │ Type   Name          Value          TTL        │
│ example.net                 │ A      www           1.2.3.4        300        │
│ example.org                 │ AAAA   api           2001:db8::1    300        │
│                             │ MX     @             mail...         3600       │
│                             │                                               │
├─────────────────────────────┤                                               │
│ Accounts                    │                                               │
│                             │                                               │
│ cloudflare.personal         │                                               │
│ cloudflare.work             │                                               │
│ namecheap.main              │                                               │
└─────────────────────────────┴───────────────────────────────────────────────┘
```

The right pane is larger.

The panes are:

1. **Domains** — upper left.
2. **Accounts/providers** — lower left.
3. **DNS records** — right.

Account identifiers should look approximately like:

```text
cloudflare.personal
cloudflare.work
namecheap.main
```

Conceptually:

```text
<provider>.<account>
```

The user already has an MVP resembling this layout.

---

## Navigation

The user wants simple, predictable keyboard navigation.

Current direction:

```text
Tab          next pane
Shift+Tab    previous pane
/            search/filter current pane
Esc          leave/clear search
↑/↓ or j/k   navigate items
Enter        select/open
```

Avoid complicated one-character modes for changing focus.

The active pane should be visually obvious.

### Search

Search behavior depends on the focused pane.

When **Domains** is focused:

```text
/ -> search/filter domains
```

When **Records** is focused:

```text
/ -> search/filter DNS records
```

Record search should match at least:

```text
record name
record type
record value
```

Initially, record search can be scoped to the currently selected zone.

Cross-zone/cross-account search can be added later.

---

# Cache-first architecture

This is an important product requirement.

The UI **must not block startup while contacting providers**.

Startup should approximately be:

```text
start application
      │
      ▼
open local DB/cache
      │
      ▼
immediately render cached accounts/zones/records
      │
      ▼
start asynchronous validation
      │
      ▼
check SOA serials
```

Cached information is displayed immediately.

Provider connectivity happens asynchronously.

---

# SOA-based freshness

The **DNS SOA serial is the primary mechanism for deciding whether a zone needs to be refreshed**.

For every cached zone:

```text
cached SOA serial
       │
       ▼
query current SOA
       │
       ├── same serial ─────► cache is current
       │
       ├── different ───────► fetch records from provider API
       │
       └── unreachable ─────► retain stale cache
```

If the serial hasn't changed, do **not** unnecessarily fetch the complete record list.

If it changed:

1. Fetch current records using the provider API.
2. Replace/update the local cached representation.
3. Store the new SOA serial.
4. Mark the zone as current.

The implementation should account for provider/DNS-specific SOA behavior rather than blindly assuming every provider behaves identically.

---

# Freshness in the UI

Cached data should initially appear visually **dimmed/gray**.

For example:

```text
example.com     gray     checking...
example.net     gray     checking...
example.org     white    current
```

After successful validation:

```text
gray -> normal/white
```

Potential internal states could be something similar to:

```go
type FreshnessState int

const (
    FreshnessUnknown FreshnessState = iota
    FreshnessChecking
    FreshnessCurrent
    FreshnessStale
    FreshnessError
)
```

This is only a design suggestion; don't treat the exact API as finalized.

---

# Critical write-safety rule

**A zone must not be modified until it has been successfully validated/refreshed during the current application session.**

If the provider cannot currently be reached:

- cached records remain visible;
- the application remains usable for browsing/searching;
- the zone remains gray/stale;
- create/update/delete operations are disabled.

Conceptually:

```text
cached zone
    │
    ▼
SOA validation successful?
    │
 ┌──┴──┐
 no    yes
 │      │
 ▼      ▼
read   writable
only
```

This prevents the user from changing DNS based on potentially stale state.

---

# Write behavior

Once the zone has been validated, record modifications can be sent to the provider.

After a successful provider API response:

```text
user edits record
      │
      ▼
provider API request
      │
      ▼
provider reports success
      │
      ▼
update local cache immediately
      │
      ▼
update TUI
```

**Do not wait for the SOA serial to change after a write.**

The provider's successful response is considered authoritative for that operation.

Do not hard-code HTTP `200` as the only success condition. Provider adapters should understand what constitutes success for their API, including other appropriate `2xx` responses or provider-specific semantics.

---

# Provider abstraction

Provider-specific behavior should not leak into the TUI.

The architecture should have an abstraction roughly around:

```text
Provider
   │
   ├── Account
   │
   └── Zone
          │
          ├── SOA
          └── Records
```

Potential provider interface responsibilities include:

```go
type Provider interface {
    Zones(ctx context.Context) ([]Zone, error)

    Records(ctx context.Context, zone Zone) ([]Record, error)

    CreateRecord(ctx context.Context, zone Zone, record Record) (...)
    UpdateRecord(ctx context.Context, zone Zone, record Record) (...)
    DeleteRecord(ctx context.Context, zone Zone, record Record) (...)
}
```

SOA validation may belong in a separate DNS/freshness layer rather than necessarily in the provider interface.

Do not assume this interface is finalized; design it carefully.

---

# Authentication

Each provider account can have its own credentials.

Example:

```text
Cloudflare
 ├── personal
 └── work

Namecheap
 └── main
```

Credentials should **not** be stored in plaintext in the application database.

Preferred model:

```text
configuration/database
        │
        │ account metadata
        ▼
provider account

OS keychain
        │
        │ secret/API token
        ▼
provider authentication
```

On macOS, investigate Apple Keychain integration.

The user is interested in requiring Touch ID/fingerprint authorization where supported.

Design this so other platforms can have suitable backends, for example Linux Secret Service/keyring and Windows Credential Manager.

---

# Local database

The exact database hasn't been selected.

It needs to persist at least:

```text
providers/accounts
zones
records
SOA serial
last successful refresh
provider-specific IDs/metadata
```

Credentials belong in the keychain, **not** in this DB.

SQLite is a natural candidate, but this is not yet a final decision.

---

# Concurrency

Background refresh should be asynchronous.

Do not make provider requests sequentially if that makes startup validation unnecessarily slow.

At the same time:

- use bounded concurrency;
- respect provider rate limits;
- use `context.Context`;
- allow cancellation on application exit;
- prevent two refresh operations from racing on the same zone;
- avoid UI goroutines directly modifying shared state.

For Bubble Tea, prefer passing refresh results back as messages.

Conceptually:

```text
             ┌─ SOA check zone A ─┐
cache ───────┼─ SOA check zone B ─┼──► Bubble Tea messages
             ├─ SOA check zone C ─┤
             └─ SOA check zone D ─┘
```

---

# Offline behavior

Provider outage must **not** make the application unusable.

For example:

```text
Cloudflare.personal
  example.com     stale
  example.net     current

Namecheap.main
  foo.net         checking
```

The user can still inspect and search cached data.

Only zones successfully validated in the current session become writable.

# Provider specific

Some providers may have unique features:

Cloudflare:
- proxy support for DNS records.
- tls configuration
- HTTP Strict Transport Security (HSTS)

# Templates

We need to support DNS record templates for common configurations.
- MX, SPF, DKIM records for mail servers (google, protonmail)
- MX, SPF, DKIM records to disable email spoofing (parking domains)
