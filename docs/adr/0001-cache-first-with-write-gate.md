# Cache-first startup with a write gate

dnscli renders the local cache immediately. In the background it runs a Serial Check per Zone through the system DNS resolver, which only colours freshness: a caching resolver can lag behind and follows public Delegation, so it cannot prove a Zone is current. A Zone becomes editable only after a Refresh from the Provider API in the current session, triggered when the user selects it. Every Apply re-checks the affected Records against the Provider before sending, and a Provider's success response is authoritative: the cache is updated from it without waiting for the serial to change.

## Considered Options

- Block startup on the Provider API: rejected, the app must be usable offline and start instantly.
- Treat a matching SOA serial as validation: rejected, resolver caching and undocumented provider serial behaviour (Cloudflare does not document when its serial changes) make it unreliable.
- Query the Provider's nameservers directly for SOA: deferred; may later make the Serial Check more accurate.
