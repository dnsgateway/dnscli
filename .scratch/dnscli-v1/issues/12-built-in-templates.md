# 12: Built-in Templates

**What to build:** On a Validated Zone, the user can apply a built-in Template: "Google mail", "Proton mail" or "parked domain". The Template asks for its parameters (for example a DKIM key or a verification token) and produces a Changeset that goes through the normal preview and Apply. Singleton Records (SPF, DMARC) are replaced, not duplicated. "Parked domain" publishes Records that block email spoofing: a null MX, `v=spf1 -all` and a DMARC reject policy.

**Blocked by:** 09

**Status:** ready-for-agent

- [ ] Templates are built in and defined in code, with typed parameters
- [ ] Applying a Template prompts for its parameters and produces a pending Changeset, shown in the normal preview
- [ ] SPF and DMARC Records already in the Zone are replaced, not duplicated
- [ ] "Parked domain" produces a null MX, `v=spf1 -all` and a DMARC reject policy
- [ ] Templates obey the write gate
- [ ] Seam 1 tests cover each Template, including singleton replacement on a Zone that already has SPF and DMARC
