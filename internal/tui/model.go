// Package tui is dnscli's Bubble Tea user interface.
//
// The top-level Model owns all UI state. Background work runs in commands
// that never touch UI state and report back only as messages.
package tui

import (
	"context"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/vault"
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
	search        [paneCount]search

	loaded   bool
	err      error // reading Accounts and Zones failed
	accounts []dns.Account
	zones    []dns.Zone
	// zoneByID indexes zones by Zone ID.
	zoneByID map[int64]dns.Zone

	// accountFilter is the Account filter: the Domains pane shows only this
	// Account's Zones. Zero means every Account.
	accountFilter int64
	// visible are the Zones the Domains pane lists, after the Account filter
	// and its search.
	visible []dns.Zone
	// shared holds the zone names, as nameKey keys, that exist in more than
	// one Account.
	shared map[string]bool

	// open is the Zone whose Records are shown; nil until the user opens one.
	open           *dns.Zone
	recordsLoading bool
	recordsErr     error // reading the open Zone's Records failed
	records        []dns.Record
	// visibleRecords are the Records the Records pane lists, after its search.
	visibleRecords []dns.Record

	// fresh is each Zone's freshness this session, by Zone ID.
	fresh map[int64]*zoneFreshness
	// checks schedules the Serial Checks.
	checks zoneQueue
	// refreshes is each Zone's Refresh state this session, by Zone ID.
	refreshes map[int64]*zoneRefresh
	// refreshQueue schedules the Refreshes.
	refreshQueue zoneQueue

	// addForm is the add-Account dialog; nil when closed.
	addForm *addAccountForm
	// notice is the latest background result shown in the status bar.
	notice notice

	// key is the data key; nil while the session is sealed.
	key *vault.Key
	// slots are the enrolled Unseal Method key slots.
	slots []vault.Slot
	// unseal is the Unseal prompt; nil when closed.
	unseal *unsealPrompt
	// afterUnseal is the action waiting for the Unseal prompt.
	afterUnseal func() tea.Cmd
	// keychain asks to enroll the Keychain; nil when closed.
	keychain *keychainConfirm
	// remove asks to remove an Account; nil when closed.
	remove *removeConfirm
	// relisting holds the IDs of the Accounts whose Zones are being re-listed.
	relisting map[int64]bool
}

// notice is a message for the status bar.
type notice struct {
	text string
	err  bool
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
	slots    []vault.Slot
	err      error
}

// recordsLoadedMsg carries a Zone's cached Records read from the Store.
type recordsLoadedMsg struct {
	zoneID  int64
	records []dns.Record
	err     error
	// opened is true when the user has just opened the Zone: once its
	// cached Records show, it is Refreshed. Otherwise a Refresh reloaded
	// them, and the cursor stays where it was.
	opened bool
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

	slots, err := m.deps.Store.KeySlots(m.ctx)
	if err != nil {
		return cacheLoadedMsg{err: err}
	}

	return cacheLoadedMsg{accounts: accounts, zones: zones, slots: slots}
}

// loadRecords reads a Zone the user has just opened from the Store.
func (m *Model) loadRecords(zoneID int64) tea.Cmd {
	return m.readRecords(zoneID, true)
}

// reloadRecords reads the open Zone's Records again after a Refresh.
func (m *Model) reloadRecords(zoneID int64) tea.Cmd {
	return m.readRecords(zoneID, false)
}

func (m *Model) readRecords(zoneID int64, opened bool) tea.Cmd {
	ctx, store := m.ctx, m.deps.Store

	return func() tea.Msg {
		records, err := store.Records(ctx, zoneID)

		return recordsLoadedMsg{zoneID: zoneID, records: records, err: err, opened: opened}
	}
}

// Update handles a message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case cacheLoadedMsg:
		if msg.err != nil && m.loaded {
			// A failed reload keeps the data already shown.
			m.notice = notice{text: "Could not reload the local database: " + msg.err.Error(), err: true}

			return m, nil
		}

		zoneID, accountID := m.cursorIDs()

		m.loaded = true
		m.err = msg.err
		m.accounts = msg.accounts
		m.zones = msg.zones
		m.zoneByID = make(map[int64]dns.Zone, len(msg.zones))

		for _, z := range msg.zones {
			m.zoneByID[z.ID] = z
		}

		if m.open != nil {
			if z, ok := m.zoneByID[m.open.ID]; ok {
				m.open = &z // a Refresh may have changed its serial
			}
		}

		m.slots = msg.slots
		m.shared = sharedNames(msg.zones)
		m.refilter(paneDomains)
		// A reload, such as after Zone discovery, may reorder the lists:
		// keep each cursor on its Zone or Account, else inside its list.
		if i := slices.IndexFunc(m.visible, func(z dns.Zone) bool { return z.ID == zoneID }); i >= 0 {
			m.cursor[paneDomains] = i
		}

		if i := slices.IndexFunc(m.accounts, func(a dns.Account) bool { return a.ID == accountID }); i >= 0 {
			m.cursor[paneAccounts] = i
		}

		for p := range paneCount {
			m.cursor[p] = min(m.cursor[p], max(m.itemCount(p)-1, 0))
		}

		return m, m.queueSerialChecks()
	case serialCheckedMsg:
		return m, m.serialChecked(msg)
	case zoneRefreshedMsg:
		return m, m.zoneRefreshed(msg)
	case unsealedMsg:
		return m, m.unsealed(msg)
	case keychainEnrolledMsg:
		m.keychainEnrolled(msg)
	case accountAddedMsg:
		return m, m.accountAdded(msg)
	case zonesDiscoveredMsg:
		return m, m.zonesDiscovered(msg)
	case accountRemovedMsg:
		return m, m.accountRemoved(msg)
	case recordsLoadedMsg:
		if m.open == nil || m.open.ID != msg.zoneID {
			return m, nil // the user has moved on to another Zone
		}

		m.recordsLoading = false
		m.recordsErr = msg.err
		m.records = msg.records
		m.refilter(paneRecords)

		if !msg.opened {
			m.cursor[paneRecords] = min(m.cursor[paneRecords], max(len(m.visibleRecords)-1, 0))

			return m, nil
		}

		m.cursor[paneRecords] = 0

		return m, m.refreshZone(*m.open)
	case tea.KeyMsg:
		if m.unseal != nil {
			return m, m.unsealKey(msg)
		}

		if m.addForm != nil {
			return m, m.addAccountKey(msg)
		}

		if m.keychain != nil {
			return m, m.keychainKey(msg)
		}

		if m.remove != nil {
			return m, m.removeAccountKey(msg)
		}

		if m.typeIntoSearch(msg) {
			return m, nil
		}

		m.notice = notice{} // a notice stays until the user moves on

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

	if a != ActionNone && a != ActionUp && a != ActionDown {
		m.search[m.focus].typing = false // the filter stays; keys are actions again
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
	case ActionAllAccounts:
		m.setAccount(0)
	case ActionSearch:
		m.startSearch()
	case ActionClose:
		m.clearSearch()
	case ActionAddAccount:
		m.openAddAccount()
	case ActionEnrollKeychain:
		return m.enrollKeychain()
	case ActionRemoveAccount:
		return m.removeAccount()
	case ActionRelistZones:
		return m.relistZones()
	case ActionRefresh:
		return m.retryRefresh()
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

// cursorIDs returns the IDs of the Zone and the Account under the cursors;
// zero for an empty list.
func (m *Model) cursorIDs() (zoneID, accountID int64) {
	if i := m.cursor[paneDomains]; i < len(m.visible) {
		zoneID = m.visible[i].ID
	}

	if i := m.cursor[paneAccounts]; i < len(m.accounts) {
		accountID = m.accounts[i].ID
	}

	return zoneID, accountID
}

func (m *Model) itemCount(p pane) int {
	switch p {
	case paneDomains:
		return len(m.visible)
	case paneAccounts:
		return len(m.accounts)
	case paneRecords:
		return len(m.visibleRecords)
	}

	return 0
}

// selectItem opens the highlighted item of the focused pane: a Zone shows
// its Records, and an Account limits the Domains pane to its Zones.
func (m *Model) selectItem() tea.Cmd {
	switch m.focus {
	case paneDomains:
		return m.openZone()
	case paneAccounts:
		if len(m.accounts) > 0 {
			m.setAccount(m.accounts[m.cursor[paneAccounts]].ID)
		}
	}

	return nil
}

// setAccount changes the Account filter. An unchanged filter changes
// nothing, so the cursor stays put.
func (m *Model) setAccount(id int64) {
	if id == m.accountFilter {
		return
	}

	m.accountFilter = id
	m.refilter(paneDomains)
	m.cursor[paneDomains] = 0
}

// refilter recomputes the items pane p lists from its unfiltered items and
// its filters. Each pane's search query lives only in m.search.
func (m *Model) refilter(p pane) {
	switch p {
	case paneDomains:
		m.visible = filterZones(m.zones, zoneFilter{account: m.accountFilter, query: m.search[paneDomains].query})
	case paneRecords:
		m.visibleRecords = filterRecords(m.records, m.search[paneRecords].query)
	}
}

// openZone shows the highlighted Zone's Records.
func (m *Model) openZone() tea.Cmd {
	if len(m.visible) == 0 {
		return nil
	}

	z := m.visible[m.cursor[paneDomains]]
	m.open = &z
	m.records = nil
	m.search[paneRecords] = search{}
	m.refilter(paneRecords)
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
