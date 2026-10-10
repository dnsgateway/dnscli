# dnscli

A terminal application for browsing and editing DNS records across several DNS hosting providers and accounts, working from a local cache.

## Language

### Hosting

**Provider**:
A kind of service dnscli manages Zones through: a DNS hosting service whose API it talks to, such as Cloudflare or GoDaddy, or a Gateway reached through the Gateway Adapter.
_Avoid_: backend, service

**Account**:
One set of credentials at a Provider, named `<provider>.<account>` (e.g. `cloudflare.work`).
_Avoid_: profile, login

**Tenant**:
The scope at a Provider that an Account's credentials act within, such as one Cloudflare account. Some credentials reach several Tenants, and the Account names the one it uses.
_Avoid_: organization, team, provider account

**Gateway**:
A dnsgate service that holds the credentials of DNS hosting services and manages records at them on a client's behalf, within the permissions of the token the client presents.
_Avoid_: DNS Router, proxy

**Gateway Adapter**:
The Provider type `dnsgate`, which talks to a Gateway rather than to a DNS hosting service. Its Account holds the Gateway's address and a token the Gateway issued, never the DNS hosting service's credentials.
_Avoid_: dnsgate provider, gateway backend

**Zone**:
A DNS zone hosted in one Account, identified by the pair (Account, zone name). The same zone name may exist in several Accounts.
_Avoid_: domain (except as a UI label)

**Record**:
A single DNS resource record inside a Zone.
_Avoid_: entry, host

**Delegation**:
The NS records at the parent zone that decide which nameservers answer for a zone name. dnscli may change Delegation; it does not manage domain registration.
_Avoid_: nameserver settings

**Live Zone**:
The Zone, among those sharing a zone name, whose Account's nameservers match the Delegation.

**Changeset**:
A set of Record additions, modifications and deletions for one Zone, previewed by the user and applied to the Provider as one operation.
_Avoid_: diff, corrections, patch

**Template**:
A built-in, named set of Records (e.g. "Google mail", "parked domain"), optionally taking parameters such as a DKIM key, that produces a Changeset when applied to a Zone. Singleton records such as SPF and DMARC are replaced, not merged.
_Avoid_: preset, profile

**Zone Status**:
A Provider-specific summary of a Zone shown in the status bar, such as mail records, DNSSEC, HSTS and whether it is the Live Zone.
_Avoid_: domain flags

### Freshness

**Serial Check**:
Comparing a Zone's cached SOA serial with the serial returned by the system DNS resolver. A cheap freshness hint, not proof.

**Refresh**:
Re-reading a Zone's Records from its Provider's API and replacing the cached copy.

**Validated Zone**:
A Zone that has been Refreshed during the current session. Only Validated Zones may be edited.

**Stale Zone**:
A cached Zone that is not yet, or could not be, validated in the current session. It can be browsed and searched but not edited.

### Session

**Unseal**:
Releasing, once per session, the key that protects every Account's API secrets. A sealed session is read-only.
_Avoid_: unlock, login

**Unseal Method**:
One way of performing Unseal, such as the macOS Keychain with Touch ID or a passphrase.

**Shortcut Profile**:
A named mapping from user actions to keys.
_Avoid_: keymap, bindings
