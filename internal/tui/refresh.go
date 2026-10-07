package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	// retryAt is when the Provider asked to be called again, after it
	// rate-limited the Refresh; zero when it did not say.
	retryAt time.Time
}

// zoneRefreshedMsg carries the result of one Refresh.
type zoneRefreshedMsg struct {
	zoneID int64
	// state is the Refresh state the Refresh was started for. A Zone
	// forgotten meanwhile has none, or a new one if its ID was reused.
	state *zoneRefresh
	err   error
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

	return ok && m.deps.Providers[a.Provider] != nil
}

// rateLimited reports whether the Provider asked not to be called again
// for a Zone yet.
func (m *Model) rateLimited(r zoneRefresh) bool {
	return !r.retryAt.IsZero() && m.deps.now().Before(r.retryAt)
}

// retryRefresh Refreshes the open Zone at the user's request, or explains
// why it cannot.
func (m *Model) retryRefresh() tea.Cmd {
	if m.open == nil {
		return nil
	}

	z := *m.open
	r := m.refreshOf(z.ID)

	if !r.pending && m.rateLimited(r) {
		m.notice = notice{text: fmt.Sprintf("Refreshing %s is rate-limited until %s.", z.Name, r.retryAt.Local().Format(time.TimeOnly))}

		return nil
	}

	return m.refreshZone(z)
}

// refreshZone Refreshes a Zone in the background once the session is
// unsealed. A Zone with a Refresh already pending, or that the Provider
// rate-limited until later, is left alone.
func (m *Model) refreshZone(z dns.Zone) tea.Cmd {
	if r := m.refreshOf(z.ID); !m.canRefresh(z) || r.pending || m.rateLimited(r) {
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

		if r.pending {
			return nil // queued again while the Unseal prompt was open
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
	ctx, s, key, newProvider, now := m.ctx, m.deps.Store, m.key, m.deps.Providers[a.Provider], m.deps.now

	return func() tea.Msg {
		return zoneRefreshedMsg{zoneID: z.ID, state: r, err: refresh(ctx, s, key, newProvider, a, z, now)}
	}
}

// refresh reads a Zone's Records from its Provider and replaces the cached
// copy with them, with the SOA serial and the Refresh time, in one transaction.
func refresh(ctx context.Context, s *store.Store, key *vault.Key, newProvider provider.Constructor, a dns.Account, z dns.Zone, now func() time.Time) error {
	p, err := accountProvider(ctx, s, key, newProvider, a)
	if err != nil {
		return err
	}

	rs, err := p.Records(ctx, z)
	if err != nil {
		return err
	}

	return s.SaveRefresh(ctx, z.ID, rs.Records, rs.Serial, now())
}

// accountProvider builds the Provider for an Account with its API token,
// opened with the data key.
func accountProvider(ctx context.Context, s *store.Store, key *vault.Key, newProvider provider.Constructor, a dns.Account) (provider.Provider, error) {
	if newProvider == nil {
		return nil, fmt.Errorf("Provider type %q is not available in this build", a.Provider)
	}

	sealed, err := s.AccountSecret(ctx, a.ID)
	if err != nil {
		return nil, err
	}

	if sealed == nil {
		return nil, errors.New("this Account has no API token")
	}

	token, err := key.Open(sealed, secretLabel(a))
	if err != nil {
		return nil, err
	}

	return newProvider(a, string(token))
}

// zoneRefreshed records a Refresh result and starts the next queued one.
// A success reloads the cache, and the open Zone's Records with it. The
// result of a Zone forgotten meanwhile, such as with its Account, is
// dropped: it usually failed with store.ErrZoneNotCached.
func (m *Model) zoneRefreshed(msg zoneRefreshedMsg) tea.Cmd {
	m.refreshQueue.running--

	r, ok := m.refreshes[msg.zoneID]
	if !ok || r != msg.state {
		return m.startRefreshes()
	}

	r.pending = false

	if msg.err != nil {
		*r = zoneRefresh{err: msg.err}

		var apiErr *provider.APIError
		if errors.As(msg.err, &apiErr) && apiErr.RetryAfter > 0 {
			r.retryAt = m.deps.now().Add(apiErr.RetryAfter)
		}

		return m.startRefreshes()
	}

	*r = zoneRefresh{validated: true}

	cmds := []tea.Cmd{m.loadCache, m.startRefreshes()}
	if m.open != nil && m.open.ID == msg.zoneID {
		cmds = append(cmds, m.reloadRecords(msg.zoneID))
	}

	return tea.Batch(cmds...)
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
	case r.err != nil && !r.retryAt.IsZero():
		return fmt.Sprintf("Refresh failed, retry at %s (%s): %v", r.retryAt.Local().Format(time.TimeOnly), retry, r.err)
	case r.err != nil:
		return fmt.Sprintf("Refresh failed (%s retries): %v", retry, r.err)
	}

	return ""
}

// marker is the one-column Refresh marker drawn after a Zone; empty when
// the Serial Check marker applies.
func (r zoneRefresh) marker() string {
	switch {
	case r.pending:
		return checkingStyle.Render("↻")
	case r.validated:
		return hintCurrentStyle.Render("✓")
	case r.err != nil:
		return errorStyle.Render("✗")
	}

	return ""
}
