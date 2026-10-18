# DNS cli

Manage DNS records across providers and accounts from your terminal.

dnscli is a Go application with an interactive terminal UI built with Bubble Tea.
It brings accounts, domains, and DNS records into one keyboard-driven workspace, with a local database that keeps cached records available when a provider is unreachable.

## Motivation

Managing DNS across personal and work accounts often means switching between provider dashboards, learning different interfaces, and waiting for remote APIs just to inspect a record.
dnscli aims to make that work faster and more consistent: open the terminal, find a domain, and manage its records in one place.

Fast access should also support careful changes. Cached data remains useful for browsing, while edits require a fresh view from the provider.

## Goals

- **One interface for multiple providers and accounts.** Work with providers such as Cloudflare and Namecheap, with separate accounts for personal and work domains.
- **Immediate access to cached data.** Render locally stored accounts, zones, and records at startup, while freshness checks run in the background.
- **Useful during outages.** Keep cached records available for inspection and search when a provider cannot be reached.
- **Careful writes.** Require a successful provider refresh in the current session before editing a zone, and recheck affected records before applying changes.
- **Protected credentials.** Encrypt provider secrets locally and support unlocking through macOS Keychain with Touch ID or a passphrase.
- **Predictable keyboard navigation.** Move between domains, accounts, and records, with search scoped to the active pane.
- **Repeatable DNS configuration.** Use record templates for common setups, including Google Workspace, Proton Mail, and parked domains.

## Installation

Install with Homebrew:

```sh
brew install dnsgateway/tap/dnscli
```

Launch the interactive interface:

```sh
dnscli
```

To see available command-line options:

```sh
dnscli --help
```

## How it works

The interface groups domains and accounts on the left, with a larger records pane on the right. Accounts can be named to distinguish their purpose, such as `cloudflare.personal`, `cloudflare.work`, or `namecheap.main`.

dnscli displays cached data immediately. Background SOA serial checks provide freshness hints; selecting a zone refreshes its records through the provider API. A successful provider refresh makes the zone editable for the current session. After the provider accepts a change, dnscli updates the local records without waiting for a DNS serial change.

Account definitions, zones, records, and encrypted credentials live in a local SQLite database. Account metadata and cached DNS data remain readable before unlocking secrets. The database holds persistent account state as well as cached records, so keep it backed up along with a usable unlock method.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for development and contribution instructions.

## License

[Mozilla Public License 2.0](LICENSE).
