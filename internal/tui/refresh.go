package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/store"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// zoneRefresh is what this session knows about one Zone's Refreshes. It is
// held in memory only and never persisted, so every session starts with
// no Zone validated.
type zoneRefresh struct {
	// pending is true from the moment a Refresh is queued until its result
	// arrives. At most one Refresh of a Zone is pending (single-flight).
	pending bool
	// validated is true once a Refresh succeeded this session, until a
	// later one fails.
	validated bool
	// err is why the latest Refresh failed.
	err error
	// notServed is true when the latest Refresh found that the Zone's
	// Provider serves no DNS Records for it, such as for a domain its
	// Account only registers while the Delegation names other
	// nameservers. The Zone is then registration only: edits made here
	// would not apply.
	notServed bool
	// retryAt is when the Provider asked to be called again, after it
	// rate-limited the Refresh or its Zone Status read; zero when it did
	// not say.
	retryAt time.Time
	// caps is what the Zone's Provider takes, as the validating Refresh
	// found it.
	caps providerCaps
}

// providerCaps is what a Provider takes beyond the Provider interface:
// its TTL range, whether TTL 1 is automatic, and the Record types it can
// proxy.
type providerCaps struct {
	ttl       dnsprovider.TTLRange
	autoTTL   bool
	proxiable []string
}

// ttlRange is the TTLs the Provider takes: dnsprovider.DefaultTTLRange when it
// did not say, and TTL dnsprovider.TTLAuto only where it is automatic.
func (c providerCaps) ttlRange() dnsprovider.TTLRange {
	r := c.ttl
	if r.Min == 0 && r.Max == 0 {
		r = dnsprovider.DefaultTTLRange
	}

	r.Auto = c.autoTTL

	return r
}

func capsOf(p dnsprovider.Provider) providerCaps {
	return providerCaps{ttl: dnsprovider.TTLRangeOf(p), autoTTL: dnsprovider.AutoTTLOf(p), proxiable: dnsprovider.ProxiableTypesOf(p)}
}

// zoneRefreshedMsg carries the result of one Refresh.
type zoneRefreshedMsg struct {
	zoneID int64
	// state is the Refresh state the Refresh was started for. A Zone
	// forgotten meanwhile has none, or a new one if its ID was reused.
	state *zoneRefresh
	// zone is the Zone as the Refresh stored it, with its serial and
	// Refresh time.
	zone dns.Zone
	// caps is what the Zone's Provider takes.
	caps providerCaps
	err  error
	// statusErr is why the Zone Status could not be read, when the
	// Refresh succeeded without it.
	statusErr error
	// serialFailed is true when the Zone's nameservers were asked for its
	// serial and did not answer.
	serialFailed bool
}

// validatedZone reports whether a Zone is a Validated Zone: Refreshed
// successfully in this session, with no Refresh in flight. It is one half
// of the write gate; the other is an unsealed session.
func (m *Model) validatedZone(id int64) bool {
	r, ok := m.refreshes[id]

	return ok && r.validated && !r.pending
}

// refreshOf returns what this session knows about a Zone's Refreshes.
func (m *Model) refreshOf(id int64) zoneRefresh {
	if r, ok := m.refreshes[id]; ok {
		return *r
	}

	return zoneRefresh{}
}

// canRefresh reports whether this build has the Provider a Zone's Account needs.
func (m *Model) canRefresh(z dns.Zone) bool {
	a, ok := m.account(z.AccountID)

	_, built := m.deps.Providers[a.Provider]

	return ok && built
}

// rateLimited reports whether the Provider asked not to be called again
// for a Zone yet.
func (m *Model) rateLimited(r zoneRefresh) bool {
	return !r.retryAt.IsZero() && m.deps.now().Before(r.retryAt)
}

// retryRefresh Refreshes the open Zone at the user's request, or explains
// why it cannot. Either way it checks the DS at the parent and the keys
// at the Zone's nameservers again, which need no secrets.
func (m *Model) retryRefresh() tea.Cmd {
	if m.open == nil {
		return nil
	}

	return tea.Batch(m.checkDNSSEC(*m.open), m.refreshOpenZone())
}

// refreshOpenZone Refreshes the open Zone, or explains why it cannot.
func (m *Model) refreshOpenZone() tea.Cmd {
	z := *m.open
	r := m.refreshOf(z.ID)

	switch {
	case !m.canRefresh(z):
		a, _ := m.account(z.AccountID)
		m.notice = notice{text: fmt.Sprintf("Cannot Refresh %s: Provider type %q is not available in this build.", z.Name, a.Provider), err: true}
	case r.pending:
		m.notice = notice{text: z.Name + " is already being Refreshed…"}
	case m.applying[z.ID]:
		m.notice = notice{text: "Changes to " + z.Name + " are being applied: Refresh it once they are."}
	case m.isOffboarding(z.ID):
		m.notice = notice{text: z.Name + " is being Offboarded."}
	case m.rateLimited(r):
		m.notice = notice{text: fmt.Sprintf("Refreshing %s is rate-limited until %s.", z.Name, r.retryAt.Local().Format(time.TimeOnly))}
	default:
		return m.refreshZone(z)
	}

	return nil
}

// refreshZone Refreshes a Zone in the background once the session is
// unsealed. A Zone with a Refresh already pending, such as one queued
// again while the Unseal prompt was open, or that the Provider rate-limited
// until later, is left alone.
func (m *Model) refreshZone(z dns.Zone) tea.Cmd {
	if !m.canRefresh(z) {
		return nil
	}

	return m.withSecrets(func() tea.Cmd {
		if m.refreshes == nil {
			m.refreshes = map[int64]*zoneRefresh{}
		}

		r, ok := m.refreshes[z.ID]
		if !ok {
			r = &zoneRefresh{}
			m.refreshes[z.ID] = r
		}

		// A Refresh during an Apply could cache Records older than the
		// ones the Apply confirms; during an Offboarding, Records of a
		// Zone it removes from the cache.
		if r.pending || m.applying[z.ID] || m.isOffboarding(z.ID) || m.rateLimited(*r) {
			return nil
		}

		r.pending = true
		m.refreshQueue.queue = append(m.refreshQueue.queue, z.ID)

		return m.startRefreshes()
	})
}

// startRefreshes starts queued Refreshes up to the concurrency limit.
func (m *Model) startRefreshes() tea.Cmd {
	var cmds []tea.Cmd

	for m.refreshQueue.running < m.deps.refreshes() && len(m.refreshQueue.queue) > 0 {
		id := m.refreshQueue.queue[0]
		m.refreshQueue.queue = m.refreshQueue.queue[1:]

		z, ok := m.zoneByID[id]
		a, found := m.account(z.AccountID)
		r, queued := m.refreshes[id]

		if !ok || !found || !queued {
			delete(m.refreshes, id) // removed from the cache meanwhile

			continue
		}

		m.refreshQueue.running++
		cmds = append(cmds, m.refreshCmd(z, a, r))
	}

	return tea.Batch(cmds...)
}

// refreshCmd Refreshes a Zone from its Provider in the background.
func (m *Model) refreshCmd(z dns.Zone, a dns.Account, r *zoneRefresh) tea.Cmd {
	ctx, s, key, providers, now := m.ctx, m.deps.Store, m.key, m.providers, m.deps.now
	q := m.serialQueryFor(z)

	return func() tea.Msg {
		res := refresh(ctx, s, key, providers, a, z, q, now)

		return zoneRefreshedMsg{zoneID: z.ID, state: r, zone: res.zone, caps: res.caps, err: res.err,
			statusErr: res.statusErr, serialFailed: res.serialFailed}
	}
}

// zoneStatusTimeout bounds the Zone Status read of a Refresh, so a slow
// one holds back saving the Records for no longer than this.
const zoneStatusTimeout = 10 * time.Second

// zoneStatusRead is the result of a Zone Status read.
type zoneStatusRead struct {
	status dnsprovider.ZoneStatus
	err    error
}

// serialRead is the result of a query for a Zone's serial.
type serialRead struct {
	serial uint32
	failed bool
}

// refreshResult is the result of refresh.
type refreshResult struct {
	// zone is the Zone as stored.
	zone dns.Zone
	// caps is what the Zone's Provider takes.
	caps providerCaps
	// statusErr is why the Zone Status could not be read.
	statusErr error
	// serialFailed is true when the Zone's nameservers did not answer.
	serialFailed bool
	err          error
}

// refresh reads a Zone's Records and, alongside them, its Zone Status from
// its Provider and its serial from its nameservers, and replaces the cached
// copy with them, with the SOA serial and the Refresh time, in one
// transaction. It returns the Zone as stored, and what its Provider takes.
//
// The serial is the one the Provider reports, else the one q gets from the
// Zone's nameservers. Without either, the cached serial is kept.
//
// A Zone Status that cannot be read does not fail the Refresh: the cached
// one is kept, and shows as older than the Refresh. Why it could not be
// read is returned as statusErr. So is one written while it was read,
// such as by a change of the Zone's settings, which it may have missed.
func refresh(ctx context.Context, s *store.Store, key *vault.Key, providers *provider.Opener, a dns.Account, z dns.Zone, q serialQuery, now func() time.Time) refreshResult {
	p, _, err := accountProvider(ctx, s, key, providers, a)
	if err != nil {
		return refreshResult{err: err}
	}

	res := refreshResult{caps: capsOf(p)}

	statusCtx, cancel := context.WithTimeout(ctx, zoneStatusTimeout)
	defer cancel()

	read := make(chan zoneStatusRead, 1)
	// The Zone Status is as new as the start of its read: one written
	// since, such as by a change of its settings, may have been missed.
	readFrom := now()

	go func() {
		status, err := dnsprovider.ZoneStatusOf(statusCtx, p, provider.ModuleZone(z))
		read <- zoneStatusRead{status: status, err: err}
	}()

	// The serial query bounds itself by serialQueryTimeout.
	serialCtx, cancelSerial := context.WithCancel(ctx)
	defer cancelSerial()

	queried := make(chan serialRead, 1)

	go func() {
		serial, failed := q.serial(serialCtx, z.Name)
		queried <- serialRead{serial: serial, failed: failed}
	}()

	rs, err := p.Records(ctx, provider.ModuleZone(z), dnsprovider.RecordFilter{})
	if err != nil {
		res.err = err

		return res
	}

	answer := <-queried
	res.serialFailed = answer.failed

	if rs.Serial == 0 {
		rs.Serial = answer.serial
	}

	status := <-read
	at := now()

	z.Status, z.StatusAt = dnsprovider.ZoneStatus{}, time.Time{}
	if status.err == nil {
		z.Status, z.StatusAt = status.status, readFrom
	}

	res.statusErr = status.err
	res.zone, res.err = s.SaveRefresh(ctx, z, provider.CacheRecords(rs.Records), rs.Serial, at)

	return res
}

// accountProvider opens the Provider for an Account with its credentials,
// unsealed with the data key, and returns them too.
//
// Credentials without a Tenant are scoped to the only one they reach,
// which is then saved with them: the Account is pinned to it. Ones
// reaching several are a *provider.TenantChoiceError, and nothing is
// asked of the Provider until one is picked (pinTenant).
func accountProvider(ctx context.Context, s *store.Store, key *vault.Key, providers *provider.Opener, a dns.Account) (dnsprovider.Provider, map[string]string, error) {
	sealed, fields, err := sealedCredentials(ctx, s, key, providers, a)
	if err != nil {
		return nil, nil, err
	}

	if fields == nil {
		return nil, nil, errors.New("this Account has no credentials")
	}

	p, scoped, err := providers.OpenScoped(ctx, a.Provider, fields)
	if err != nil {
		return nil, nil, err
	}

	if !maps.Equal(scoped, fields) {
		// Not pinned now, it is the next time it is used.
		_, _ = replaceCredentials(ctx, s, key, a, sealed, scoped)
	}

	return p, scoped, nil
}

// accountReach is an Account's Provider, within its own Tenant, and the
// one acting on the Zones of every Tenant its credentials reach: an
// Account's Zones are those of every one, but it Onboards, and Offboards,
// only within its own.
type accountReach struct {
	own    dnsprovider.Provider
	fields map[string]string
	// every lists the Zones of every Tenant; nil when own does, as the
	// credentials reach one Tenant, or none.
	every dnsprovider.Provider
	// everyErr is why the Tenants could not be listed: whether own lists
	// every Zone is then unknown.
	everyErr error
}

// lister is the Provider listing the Account's Zones as far as known.
func (r accountReach) lister() dnsprovider.Provider {
	if r.every != nil {
		return r.every
	}

	return r.own
}

// openReach opens an Account's Provider, as accountProvider does, and the
// one acting on every Tenant its credentials reach.
func openReach(ctx context.Context, s *store.Store, key *vault.Key, providers *provider.Opener, a dns.Account) (accountReach, error) {
	p, fields, err := accountProvider(ctx, s, key, providers, a)
	if err != nil {
		return accountReach{}, err
	}

	every, err := providers.EveryTenant(ctx, a.Provider, fields)

	return accountReach{own: p, fields: fields, every: every, everyErr: err}, nil
}

// replaceCredentials seals fields as an Account's credentials in place of
// sealed, the ones read, unless they were saved again meanwhile. It
// reports whether they were replaced.
func replaceCredentials(ctx context.Context, s *store.Store, key *vault.Key, a dns.Account, sealed []byte, fields map[string]string) (bool, error) {
	resealed, err := key.Seal(provider.EncodeCredentials(fields), secretLabel(a))
	if err != nil {
		return false, err
	}

	return s.ReplaceAccountSecret(ctx, a.ID, sealed, resealed)
}

// accountCredentials are an Account's credentials, unsealed with the data
// key and read into its Provider's field map; nil when it has none.
func accountCredentials(ctx context.Context, s *store.Store, key *vault.Key, providers *provider.Opener, a dns.Account) (map[string]string, error) {
	_, fields, err := sealedCredentials(ctx, s, key, providers, a)

	return fields, err
}

// sealedCredentials are an Account's credentials as sealed, and read
// into its Provider's field map; nil when it has none.
func sealedCredentials(ctx context.Context, s *store.Store, key *vault.Key, providers *provider.Opener, a dns.Account) ([]byte, map[string]string, error) {
	sealed, err := s.AccountSecret(ctx, a.ID)
	if err != nil || sealed == nil {
		return nil, nil, err
	}

	plain, err := key.Open(sealed, secretLabel(a))
	if err != nil {
		return nil, nil, err
	}

	fields, err := providers.Credentials(a.Provider, plain)

	return sealed, fields, err
}

// zoneRefreshed records a Refresh result and starts the next queued one.
// A success updates the Zone in place, and reloads the open Zone's Records. The
// result of a Zone forgotten meanwhile, such as with its Account, is
// dropped: it usually failed with store.ErrZoneNotCached.
func (m *Model) zoneRefreshed(msg zoneRefreshedMsg) tea.Cmd {
	m.refreshQueue.running--

	r, ok := m.refreshes[msg.zoneID]
	if !ok || r != msg.state {
		return m.startRefreshes()
	}

	if msg.err == nil {
		m.serialQueried(msg.zoneID, msg.serialFailed)
	}

	if msg.err != nil {
		*r = zoneRefresh{err: msg.err, retryAt: m.retryAt(msg.err), notServed: errors.Is(msg.err, dnsprovider.ErrNotServed)}

		return m.startRefreshes()
	}

	// A rate-limited Zone Status read holds back the next Refresh too.
	*r = zoneRefresh{validated: true, retryAt: m.retryAt(msg.statusErr), caps: msg.caps}
	m.updateZone(msg.zone)

	cmds := []tea.Cmd{m.startRefreshes()}
	if m.open != nil && m.open.ID == msg.zoneID {
		cmds = append(cmds, m.reloadRecords(msg.zoneID))
	}

	return tea.Batch(cmds...)
}

// tenantUse Refreshes the Zone again, as it is by then.
func (msg zoneRefreshedMsg) tenantUse(m *Model) (int64, func() tea.Cmd, error) {
	z, ok := m.zoneByID[msg.zoneID]
	if !ok {
		return 0, nil, nil
	}

	return z.AccountID, func() tea.Cmd {
		if z, ok := m.zoneByID[z.ID]; ok {
			return m.refreshZone(z)
		}

		return nil
	}, msg.err
}

// retryAt is when the Provider asked to be called again, after err;
// zero when it did not say.
func (m *Model) retryAt(err error) time.Time {
	var apiErr *dnsprovider.APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		return m.deps.now().Add(apiErr.RetryAfter)
	}

	return time.Time{}
}

// updateZone replaces a cached Zone, such as with the serial and Refresh
// time a Refresh stored, everywhere the session holds it. A changed serial
// is compared again with the one its Serial Check returned.
func (m *Model) updateZone(z dns.Zone) {
	i := slices.IndexFunc(m.zones, func(c dns.Zone) bool { return c.ID == z.ID })
	if i < 0 {
		return
	}

	m.zones[i] = z
	m.zoneByID[z.ID] = z

	if m.open != nil && m.open.ID == z.ID {
		m.open = &z
	}

	if f, ok := m.fresh[z.ID]; ok {
		f.recompare(z.Serial)
	}

	m.refilter(paneDomains)
}

// describe explains a Zone's Refresh state in the status bar; empty when
// it has none. A failure leads with how to retry, so a long error cut at
// the edge of the terminal keeps it.
func (r zoneRefresh) describe(retry string) string {
	switch {
	case r.pending:
		return "refreshing…"
	case r.validated:
		return "validated this session"
	case r.notServed:
		return fmt.Sprintf("%s: its Provider serves no DNS Records for it, so changes made here would not apply (%s Refreshes)",
			registrationOnly, retry)
	case r.err != nil && !r.retryAt.IsZero():
		return fmt.Sprintf("Refresh failed, retry at %s (%s): %v", r.retryAt.Local().Format(time.TimeOnly), retry, r.err)
	case r.err != nil:
		return fmt.Sprintf("Refresh failed (%s retries): %v", retry, r.err)
	}

	return ""
}

// registrationOnly says a Zone its Provider serves no DNS Records for.
const registrationOnly = "registration only"

// marker is the one-column Refresh marker drawn after a Zone; empty when
// the Serial Check marker applies.
func (r zoneRefresh) marker() string {
	switch {
	case r.pending:
		return checkingStyle.Render("↻")
	case r.validated:
		return hintCurrentStyle.Render("✓")
	case r.notServed:
		return hintStaleStyle.Render("⌀")
	case r.err != nil:
		return errorStyle.Render("✗")
	}

	return ""
}
