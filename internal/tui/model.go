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

// Model is the top-level Bubble Tea model.
type Model struct {
	deps Deps

	ctx    context.Context
	cancel context.CancelFunc

	width, height int

	loaded   bool
	err      error
	accounts []dns.Account
	zones    []dns.Zone
}

// New builds the model. All background work derives its context from ctx,
// and quitting cancels it.
func New(ctx context.Context, deps Deps) *Model {
	ctx, cancel := context.WithCancel(ctx)

	return &Model{
		deps:   deps,
		ctx:    ctx,
		cancel: cancel,
		width:  80,
		height: 24,
	}
}

// cacheLoadedMsg carries the cached data read from the Store at startup.
type cacheLoadedMsg struct {
	accounts []dns.Account
	zones    []dns.Zone
	err      error
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
	}

	return m, nil
}
