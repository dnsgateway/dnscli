# 02: Namecheap Changeset Apply

**What to build:** A user can add, modify and delete Records in a Namecheap Zone, and apply Templates to it, through the usual Changeset preview and Safe Apply. Namecheap's `setHosts` replaces a domain's whole host list, so Apply re-reads that list, applies the Changeset to it, and writes the full result in one call. Every host and setting the user did not change survives, and a Changeset succeeds or fails as a whole. Covers user stories 22–31 of the spec.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] Apply first reads the current host list and `EmailType` with getHosts. That read, not the cached Records, is what the changes are applied to
- [ ] Each change's Before is matched by HostId when it has one, otherwise by (type, name, value). A Before that is not in the current list fails the whole Changeset with a conflict, and setHosts is not called
- [ ] Adding or modifying a type other than A, AAAA, CNAME, MX, TXT, NS (not at the apex) and CAA fails the whole Changeset with a clear message before any write. SRV is refused until ticket 03 confirms its form shape
- [ ] The full resulting list goes in a single form-encoded POST of `namecheap.domains.dns.setHosts` (`HostName1`, `RecordType1`, `Address1`, `MXPref1`, `TTL1`, …) with the unchanged `EmailType`
- [ ] Hosts the Changeset does not touch are sent back exactly as read, including URL, URL301, FRAME and ALIAS hosts with their TTLs and MX prefs
- [ ] Every change shares the one call's result: all succeed, or all fail with Namecheap's error. Results are in Changeset order
- [ ] On success, each added or modified ChangeResult's Record is the Record as sent; the next Refresh picks up Namecheap's new HostIds
- [ ] The "not supported yet" Apply from ticket 01 is gone
- [ ] Seam 2 tests cover add, modify, delete and several changes at once, checking that setHosts received the full list with every untouched host (including a URL redirect) and the original EmailType; a rejected setHosts failing every change with its error; and an unsupported write type and a missing Before refused without calling setHosts
