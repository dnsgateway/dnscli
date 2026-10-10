# DS records may be written at the registrar; the Delegation's NS records stay read-only

This amends ADR-0004. The DS records are part of the Delegation: they sit in the parent zone next to its NS records, and turning DNSSEC on or off needs them changed at the registrar. dnscli may therefore write a Zone's DS records through a Registrar Adapter, and only after the user confirms the exact set it will publish. The Delegation's NS records stay read-only, and registering names (renewals, WHOIS, transfers) stays out of scope.

The first Registrar Adapter is no longer Namecheap, which has no DNSSEC API. DS writes start with Porkbun, which takes the DS form, then Gandi, which takes the key form. Changes to the Delegation's NS records still wait for their own registrar support.
