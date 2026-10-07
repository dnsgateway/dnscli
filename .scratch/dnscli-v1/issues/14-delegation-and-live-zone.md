# 14: Delegation and Live Zone

**What to build:** For each zone name, dnscli resolves the Delegation (the parent NS) through the system resolver and compares it with each Account's expected nameservers. The Zone that matches is marked as the Live Zone in the Domains pane. A read-only Delegation view shows the delegated nameservers next to the ones the Account expects. Per ADR-0004, Delegation cannot be changed in v1.

**Blocked by:** 04, 06

**Status:** ready-for-agent

- [ ] The parent NS is resolved in the background through the injected resolver
- [ ] Among Zones that share a zone name, the one whose Account's nameservers match the Delegation is marked as the Live Zone
- [ ] A read-only Delegation view shows the delegated nameservers and the Account's expected nameservers
- [ ] A resolver failure leaves the Live Zone unknown and does not raise an error dialog
- [ ] The Live Zone is also shown in the Zone Status bar, if 13 has already landed
- [ ] Seam 1 tests cover a match, a mismatch, a zone name shared by two Accounts, and a resolver failure
