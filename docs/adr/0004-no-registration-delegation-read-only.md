---
status: accepted, amended by ADR-0007 (DS writes at the registrar)
---

# Registration out of scope; Delegation read-only in v1

dnscli manages DNS hosting, not domain registration (no renewals, WHOIS, transfers). Delegation is in scope but v1 only displays it and marks the Live Zone. Changing NS requires registrar adapters (the DNS host is often not the registrar, and Cloudflare cannot act as one), so that arrives later, starting with Namecheap.
