# Own provider module, Cloudflare first

Providers live in a dnscli-owned module behind a Changeset-based Provider interface, with per-record CRUD against each Provider's REST API. Cloudflare is implemented first, on top of `cloudflare-go`.

dnscontrol was considered as the MVP provider layer and set aside for Cloudflare: it is desired-state only (diffs a full zone, deletes anything unlisted unless purging is disabled), hides record IDs, cannot read Cloudflare zone settings such as HSTS, has no API stability promise, prints to stdout, and requires Go 1.26. It remains a reference for other Providers and for registrar support.
