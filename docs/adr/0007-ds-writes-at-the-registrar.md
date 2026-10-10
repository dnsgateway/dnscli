# DS records may be written at the registrar; NS Delegation stays read-only

This amends ADR-0004. The DS records are part of the Delegation: they sit in the parent zone next to the NS records, and turning DNSSEC on or off needs them changed at the registrar. dnscli may therefore write a Zone's DS records through a registrar adapter, and only after the user confirms the exact set it will publish. NS Delegation stays read-only, and domain registration itself (renewals, WHOIS, transfers) stays out of scope.

The first registrar adapter is no longer Namecheap, which has no DNSSEC API. DS writes start with Porkbun, which takes the DS form, then Gandi, which takes the key form. NS changes still wait for their own registrar support.
