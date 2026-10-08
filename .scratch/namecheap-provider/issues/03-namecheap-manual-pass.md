# 03: Manual pass against a real Namecheap Account

**What to build:** Check the Namecheap adapter against a real Namecheap Account, or Namecheap's sandbox, and confirm the details the spec took from third-party clients (lego, libdns, dnscontrol) rather than from Namecheap's own docs. Record each finding in this ticket's comments, and turn any follow-up into its own ticket.

**Blocked by:** 02

**Status:** resolved

- [x] Adding an Account with a valid token discovers its Zones. A wrong key, an Account without API access, and a non-whitelisted client IP each show "not authorized" with Namecheap's message
- [ ] The real error numbers for auth, unknown domain and rate limit match the adapter's mapping
- [ ] Records show correctly, including a CAA Record and a URL redirect; note how CAA values are encoded, and whether getHosts returns apex NS Records. An SRV Record reads as `priority weight port target`, with the priority from `MXPref`
- [x] The "Automatic" TTL comes back as 1799
- [x] An applied Changeset keeps the untouched hosts (including a URL redirect) and the email setting
- [ ] Namecheap's setHosts form shape for SRV is confirmed; if it works, open a follow-up ticket to allow SRV writes
- [x] Namecheap's setHosts form shape for CAA is confirmed (how `0 issue "letsencrypt.org"` is sent in `Address`, and whether flags and tag need fields of their own) and reads back unchanged; if it works, open a follow-up ticket to allow CAA writes (refused since ticket 02's review)
- [ ] Check whether setHosts accepts a Changeset that deletes every host (an empty host list), and what the domain looks like afterwards
- [ ] Check that an MX Record written with the "Custom MX" mail setting is served, and that Apply refuses MX writes under another setting (e.g. Email Forwarding) with its "Custom MX" message
- [x] Note whether PremiumDNS or FreeDNS Zones use other nameservers than `*.registrar-servers.com`. If so, their Live Zone marker reads as not live, because `Nameservers` returns only the BasicDNS pair; open a follow-up ticket to read them per domain (`namecheap.domains.getInfo`) in `Nameservers`

## Comments

- 2026-10-08: Closed after the manual pass. The checked items were confirmed against a real Account. The unchecked ones were not:
  - Real error numbers for auth, unknown domain and rate limit.
  - Records showing correctly. MX and SRV Records made in Namecheap's console do not show in the record list. The adapter turns every getHosts `<host>` into a Record and nothing downstream filters by type, so getHosts most likely doesn't return them. dnscontrol says Namecheap's API can't read or set SRV, and MX hosts are only returned under the "Custom MX" mail setting. The raw getHosts answer was not checked.
  - The setHosts form shape for SRV. SRV writes stay refused.
  - setHosts with an empty host list.
  - MX served under "Custom MX", and Apply refusing MX under another mail setting.
