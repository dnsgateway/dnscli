# 06: Background Serial Check

**What to build:** At startup, dnscli runs a Serial Check for every cached Zone in the background, through the system DNS resolver, with bounded concurrency. Each Zone's freshness is coloured as it goes: checking, hint-current or hint-stale/error. Per ADR-0001 the Serial Check is only a hint and never makes a Zone editable.

This ticket introduces the resolver as an injected dependency, with a fake that returns scripted SOA answers in tests.

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] The resolver is injected into the model. `main` wires the system resolver, and tests use a fake
- [ ] Every cached Zone gets a Serial Check at startup, and startup rendering is never blocked
- [ ] Concurrency is bounded and configurable
- [ ] Zones show checking, hint-current (cached serial matches), or hint-stale/error (serial differs, or the resolver fails)
- [ ] A hint-current Zone is still not a Validated Zone
- [ ] Results reach the UI only as Bubble Tea messages
- [ ] Seam 1 tests cover matching, differing and failing serials
