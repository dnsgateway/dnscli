# 12: Built-in Templates

**What to build:** On a Validated Zone, the user can apply a built-in Template: "Google mail", "Proton mail" or "parked domain". The Template asks for its parameters (for example a DKIM key or a verification token) and produces a Changeset that goes through the normal preview and Apply. Singleton Records (SPF, DMARC) are replaced, not duplicated. "Parked domain" publishes Records that block email spoofing: a null MX, `v=spf1 -all` and a DMARC reject policy.

**Blocked by:** 09

**Status:** resolved

- [x] Templates are built in and defined in code, with typed parameters
- [x] Applying a Template prompts for its parameters and produces a pending Changeset, shown in the normal preview
- [x] SPF and DMARC Records already in the Zone are replaced, not duplicated
- [x] "Parked domain" produces a null MX, `v=spf1 -all` and a DMARC reject policy
- [x] Templates obey the write gate
- [x] Seam 1 tests cover each Template, including singleton replacement on a Zone that already has SPF and DMARC

## Comments

- 2026-10-07: Implemented up to 5eb6282. Review fixes end at 1ff135d.
  - `t` in the Records pane opens "Apply a Template". Pure Templates live in `internal/template`.
  - Values follow Google's and Proton's own docs and the GOV.UK parked-domain guidance. The DMARC default is `p=none`; Proton recommends quarantine.
  - A Template owns these sets: SPF, DMARC, the apex MX set, the Proton verification TXT and its DKIM names. Parked owns everything under `_domainkey`. Records already correct are kept; a single leftover becomes a Modify, otherwise Delete + Add.
  - An existing DMARC is prefilled and only `p=`/`rua=` change. A weaker policy needs a second Enter.
  - A clash with pending changes stages nothing and lists the clashing Records.
  - TTL is 1 only for Providers with `provider.AutoTTL` (Cloudflare), else 3600. The Record form rejects 1/"auto" elsewhere.
  - The help overlay (22 rows) is still cut off on 80x24.
