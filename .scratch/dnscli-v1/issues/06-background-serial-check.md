# 06: Background Serial Check

**What to build:** At startup, dnscli runs a Serial Check for every cached Zone in the background, through the system DNS resolver, with bounded concurrency. Each Zone's freshness is coloured as it goes: checking, hint-current or hint-stale/error. Per ADR-0001 the Serial Check is only a hint and never makes a Zone editable.

This ticket introduces the resolver as an injected dependency, with a fake that returns scripted SOA answers in tests.

**Blocked by:** 01

**Status:** resolved

- [x] The resolver is injected into the model. `main` wires the system resolver, and tests use a fake
- [x] Every cached Zone gets a Serial Check at startup, and startup rendering is never blocked
- [x] Concurrency is bounded and configurable
- [x] Zones show checking, hint-current (cached serial matches), or hint-stale/error (serial differs, or the resolver fails)
- [x] A hint-current Zone is still not a Validated Zone
- [x] Results reach the UI only as Bubble Tea messages
- [x] Seam 1 tests cover matching, differing and failing serials

## Comments

- 2026-10-07: Implemented in bb7f275..ad7b0a3. Review fixes are in the 06/08/15 batch ending d6153a7.
  - The resolver interface is `SOA(ctx, zone) (uint32, error)`, on the TUI side. Ticket 14 adds NS beside it.
  - The real adapter uses miekg/dns v1.1.72, the last release on Go 1.24. It re-reads resolv.conf when the file changes, or 5s after an error.
  - `--serial-checks` sets how many checks run at once (default 8).
  - Domains markers: `…` checking, `≈` hint-current, `!` hint-stale, `?` error.
  - When a reload changes a Zone's cached serial, it is compared again, and failed checks are re-queued once per reload.
