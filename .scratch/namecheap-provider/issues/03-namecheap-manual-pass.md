# 03: Manual pass against a real Namecheap Account

**What to build:** Check the Namecheap adapter against a real Namecheap Account, or Namecheap's sandbox, and confirm the details the spec took from third-party clients (lego, libdns, dnscontrol) rather than from Namecheap's own docs. Record each finding in this ticket's comments, and turn any follow-up into its own ticket.

**Blocked by:** 02

**Status:** ready-for-human

- [ ] Adding an Account with a valid token discovers its Zones. A wrong key, an Account without API access, and a non-whitelisted client IP each show "not authorized" with Namecheap's message
- [ ] The real error numbers for auth, unknown domain and rate limit match the adapter's mapping
- [ ] Records show correctly, including a CAA Record and a URL redirect; note how CAA values are encoded, and whether getHosts returns apex NS Records
- [ ] The "Automatic" TTL comes back as 1799
- [ ] An applied Changeset keeps the untouched hosts (including a URL redirect) and the email setting
- [ ] Namecheap's setHosts form shape for SRV is confirmed; if it works, open a follow-up ticket to allow SRV writes
- [ ] Note whether PremiumDNS or FreeDNS Zones use other nameservers than `*.registrar-servers.com`; if so, open a follow-up ticket for them in `Nameservers`
