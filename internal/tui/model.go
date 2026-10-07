// Package tui is dnscli's Bubble Tea user interface.
//
// The top-level Model owns all UI state. Background work runs in commands
// that never touch UI state and report back only as messages.
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// pane identifies one of the three panes, in Tab order.
type pane int

const (
	paneDomains pane = iota
	paneAccounts
	paneRecords
	paneCount
)

// Model is the top-level Bubble Tea model.
type Model struct {
	deps       Deps
	keys       keymap
	profileErr error // the injected Shortcut Profile was invalid

	ctx    context.Context
	cancel context.CancelFunc

	width, height int
	focus         pane
	cursor        [paneCount]int
	help          bool // help overlay shown

	loaded   bool
	err      error // reading Accounts and Zones failed
	accounts []dns.Account
	zones    []dns.Zone

	// open is the Zone whose Records are shown; nil until the user opens one.
	open           *dns.Zone
	recordsLoading bool
	recordsErr     error // reading the open Zone's Records failed
	records        []dns.Record
}

// New builds the model. All background work derives its context from ctx,
// and quitting cancels it.
func New(ctx context.Context, deps Deps) *Model {
	ctx, cancel := context.WithCancel(ctx)
	profile, profileErr := deps.shortcuts()

	return &Model{
		deps:       deps,
		keys:       newKeymap(profile),
		profileErr: profileErr,
		ctx:        ctx,
		cancel:     cancel,
		width:      80,
		height:     24,
	}
}

// Context is the app's root context. All background work derives from it,
// and it is cancelled when the user quits.
func (m *Model) Context() context.Context {
	return m.ctx
}

// cacheLoadedMsg carries the cached data read from the Store at startup.
type cacheLoadedMsg struct {
	accounts []dns.Account
	zones    []dns.Zone
	err      error
}

// recordsLoadedMsg carries a Zone's cached Records read from the Store.
type recordsLoadedMsg struct {
	zoneID  int64
	records []dns.Record
	err     error
}

// Init loads the cached data from the Store. It never touches the network.
func (m *Model) Init() tea.Cmd {
	return m.loadCache
}

func (m *Model) loadCache() tea.Msg {
	accounts, err := m.deps.Store.Accounts(m.ctx)
	if err != nil {
		return cacheLoadedMsg{err: err}
	}

	zones, err := m.deps.Store.Zones(m.ctx)
	if err != nil {
		return cacheLoadedMsg{err: err}
	}

	return cacheLoadedMsg{accounts: accounts, zones: zones}
}

func (m *Model) loadRecords(zoneID int64) tea.Cmd {
	ctx, store := m.ctx, m.deps.Store

	return func() tea.Msg {
		records, err := store.Records(ctx, zoneID)

		return recordsLoadedMsg{zoneID: zoneID, records: records, err: err}
	}
}

// Update handles a message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case cacheLoadedMsg:
		m.loaded = true
		m.err = msg.err
		m.accounts = msg.accounts
		m.zones = msg.zones
	case recordsLoadedMsg:
		if m.open == nil || m.open.ID != msg.zoneID {
			return m, nil // the user has moved on to another Zone
		}

		m.recordsLoading = false
		m.recordsErr = msg.err
		m.records = msg.records
		m.cursor[paneRecords] = 0
	case tea.KeyMsg:
		return m, m.handle(m.keys.action(msg.String()))
	}

	return m, nil
}

// handle performs a user Action.
func (m *Model) handle(a Action) tea.Cmd {
	if a == ActionQuit {
		m.cancel()

		return tea.Quit
	}

	if m.help {
		// The help overlay captures every other Action except closing it.
		switch a {
		case ActionHelp, ActionClose:
			m.help = false
		}

		return nil
	}

	switch a {
	case ActionHelp:
		m.help = true
	case ActionNextPane:
		m.focus = (m.focus + 1) % paneCount
	case ActionPrevPane:
		m.focus = (m.focus + paneCount - 1) % paneCount
	case ActionUp:
		m.move(-1)
	case ActionDown:
		m.move(+1)
	case ActionSelect:
		return m.selectItem()
	}

	return nil
}

// move moves the focused pane's cursor by delta, staying inside the list.
func (m *Model) move(delta int) {
	n := m.itemCount(m.focus)
	if n == 0 {
		return
	}

	m.cursor[m.focus] = min(max(m.cursor[m.focus]+delta, 0), n-1)
}

func (m *Model) itemCount(p pane) int {
	switch p {
	case paneDomains:
		return len(m.zones)
	case paneAccounts:
		return len(m.accounts)
	case paneRecords:
		return len(m.records)
	}

	return 0
}

// selectItem opens the highlighted item of the focused pane.
func (m *Model) selectItem() tea.Cmd {
	if m.focus != paneDomains || len(m.zones) == 0 {
		return nil
	}

	z := m.zones[m.cursor[paneDomains]]
	m.open = &z
	m.records = nil
	m.recordsLoading = true
	m.recordsErr = nil

	return m.loadRecords(z.ID)
}

func (m *Model) account(id int64) (dns.Account, bool) {
	for _, a := range m.accounts {
		if a.ID == id {
			return a, true
		}
	}

	return dns.Account{}, false
}
