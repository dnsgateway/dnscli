// Package tui is dnscli's Bubble Tea user interface.
//
// The top-level Model owns all UI state. Background work runs in commands
// that never touch UI state and report back only as messages.
package tui

import (
	"context"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
	"github.com/sergelogvinov/dnscli/internal/vault"
)

// pane identifies one of the three panes, in Tab order.
type pane int

const (
	paneDomains pane = iota
	paneRecords
	paneAccounts
	paneCount
)

// paneNames are the panes' titles.
var paneNames = [paneCount]string{paneDomains: "Domains", paneRecords: "Records", paneAccounts: "Accounts"}

// Model is the top-level Bubble Tea model.
type Model struct {
	deps       Deps
	keys       keymap
	profileErr error // the injected Shortcut Profile was invalid

	// providers opens the Accounts' Providers from deps.Providers, and
	// keeps what it asked them for the session.
	providers *provider.Opener

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
	// rows are the open Zone's Records with its pending Changeset laid
	// over them, as the Records pane lists them before its search.
	rows []recordRow
	// visibleRecords are the rows the Records pane lists, after its search.
	visibleRecords []recordRow

	// fresh is each Zone's freshness this session, by Zone ID.
	fresh map[int64]*zoneFreshness
	// checks schedules the Serial Checks.
	checks zoneQueue
	// refreshes is each Zone's Refresh state this session, by Zone ID.
	refreshes map[int64]*zoneRefresh
	// refreshQueue schedules the Refreshes.
	refreshQueue zoneQueue
	// serialFailed is when each Zone's nameservers last failed to answer
	// the query for its serial, by Zone ID.
	serialFailed map[int64]time.Time
	// serialPolls are the polls for the new serial after an Apply, by
	// Zone ID.
	serialPolls map[int64]*serialPoll
	// delegations is each zone name's Delegation this session, by nameKey.
	delegations map[string]*delegation
	// parentDS is what this session found of the DS each zone name's
	// parent serves, by nameKey.
	parentDS map[string]*dsCheck
	// signingKeys is what this session read of each Zone's key-signing
	// keys from its own nameservers, by Zone ID.
	signingKeys map[int64]*keyCheck
	// showKeys shows the open Zone's key-signing keys and their DS, to
	// copy into a registrar's panel.
	showKeys bool
	// lookups schedules the Delegation lookups, the DS Checks and the key
	// lookups, under one limit.
	lookups workQueue[lookupJob]

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
	// changesets are the pending Changesets, by Zone ID. They are held in
	// memory only, for this session.
	changesets map[int64]*dns.Changeset
	// applying holds the IDs of the Zones whose Changeset is being applied.
	applying map[int64]bool
	// recordForm adds or edits a Record; nil when closed.
	recordForm *recordForm
	// templateForm applies a Template; nil when closed.
	templateForm *templateForm
	// preview shows a pending Changeset; nil when closed.
	preview *changesetPreview

	// relisting holds the IDs of the Accounts whose Zones are being re-listed.
	relisting map[int64]bool
	// relistAll is the re-listing of every Account under way; nil when none is.
	relistAll *relistBatch
	// tenantPick asks which Tenant an Account acts within; nil when
	// closed.
	tenantPick *tenantPick
	// zoneEdit is the Edit Zone dialog, which changes a Zone's DNSSEC,
	// HSTS or minimum TLS version and shows its Delegation; nil when
	// closed.
	zoneEdit *zoneEditForm
	// onboard Onboards a zone name in an Account; nil when closed.
	onboard *onboardDialog
	// offboard Offboards a Zone; nil when closed.
	offboard *offboardDialog
	// offboarding holds the Account ID of each Zone being Offboarded, by
	// Zone ID, until the outcome arrives, whether or not the dialog is
	// still open.
	offboarding map[int64]int64
	// editAfterPick is the ID of the Account whose edit dialog was asked
	// for while the Tenant picker was open; zero when none was. It opens
	// once the picker closes, rather than over it.
	editAfterPick int64
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
		providers:  provider.NewOpener(deps.Providers),
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

	// The session compares nameservers as nameSet sets: normalise them
	// once here rather than on every View.
	for i, z := range zones {
		if len(z.Nameservers) > 0 {
			zones[i].Nameservers = nameSet(z.Nameservers)
		}
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
	model, cmd := m.update(msg)

	// A use of an Account's Provider that needs a Tenant asks for it,
	// over whatever the use reported.
	m.askTenant(msg)

	return model, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

		return m, tea.Batch(m.queueSerialChecks(), m.queueLookups())
	case delegationMsg:
		m.recordDelegation(msg.name, msg.ns, msg.err)

		return m, m.lookupAnswered()
	case dsCheckedMsg:
		m.recordDSCheck(msg)

		return m, m.lookupAnswered()
	case keysCheckedMsg:
		m.recordKeysCheck(msg)

		return m, m.lookupAnswered()
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
	case editAccountMsg:
		if m.tenantPick != nil {
			m.editAfterPick = msg.account.ID

			return m, nil
		}

		m.openEditAccount(msg)
	case accountEditedMsg:
		return m, m.accountEdited(msg)
	case accountRemovedMsg:
		return m, m.accountRemoved(msg)
	case serialPolledMsg:
		return m, m.serialPolled(msg)
	case serialSavedMsg:
		m.serialSaved(msg)

		return m, nil
	case changesAppliedMsg:
		return m, m.changesApplied(msg)
	case tenantPinnedMsg:
		return m, m.tenantPinned(msg)
	case zoneEditOpenedMsg:
		m.zoneEditOpened(msg)
	case zoneSettingsSavedMsg:
		m.zoneSettingsSaved(msg)
	case onboardOpenedMsg:
		return m, m.onboardOpened(msg)
	case onboardTenantNamedMsg:
		m.onboardTenantNamed(msg)
	case zoneOnboardedMsg:
		return m, m.zoneOnboarded(msg)
	case offboardOpenedMsg:
		m.offboardOpened(msg)
	case zoneOffboardedMsg:
		return m, m.zoneOffboarded(msg)
	case recordsLoadedMsg:
		if m.open == nil || m.open.ID != msg.zoneID {
			return m, nil // the user has moved on to another Zone
		}

		m.recordsLoading = false
		m.recordsErr = msg.err
		m.records = msg.records
		m.rebuildRecordRows()

		if !msg.opened {
			return m, nil
		}

		m.cursor[paneRecords] = 0

		return m, tea.Batch(m.refreshZone(*m.open), m.checkDNSSEC(*m.open))
	case tea.KeyMsg:
		if m.unseal != nil {
			return m, m.unsealKey(msg)
		}

		if m.addForm != nil {
			return m, m.addAccountKey(msg)
		}

		if m.tenantPick != nil {
			return m, m.tenantPickKey(msg)
		}

		if m.keychain != nil {
			return m, m.keychainKey(msg)
		}

		if m.remove != nil {
			return m, m.removeAccountKey(msg)
		}

		if m.zoneEdit != nil {
			return m, m.zoneEditKey(msg)
		}

		if m.onboard != nil {
			return m, m.onboardKey(msg)
		}

		if m.offboard != nil {
			return m, m.offboardKey(msg)
		}

		if m.recordForm != nil {
			return m, m.recordFormKey(msg)
		}

		if m.templateForm != nil {
			return m, m.templateFormKey(msg)
		}

		if m.preview != nil {
			return m, m.previewKey(msg)
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

	if m.showKeys {
		// So does the keys overlay.
		switch a {
		case ActionShowKeys, ActionClose:
			m.showKeys = false
		}

		return nil
	}

	switch a {
	case ActionNone, ActionUp, ActionDown, ActionPageUp, ActionPageDown:
	default:
		m.search[m.focus].typing = false // the filter stays; keys are actions again
	}

	switch a {
	case ActionHelp:
		m.help = true
	case ActionShowKeys:
		m.openKeys()
	case ActionNextPane:
		m.focus = (m.focus + 1) % paneCount
	case ActionPrevPane:
		m.focus = (m.focus + paneCount - 1) % paneCount
	case ActionPaneRight:
		m.focus = paneRecords
	case ActionPaneLeft:
		if m.focus == paneRecords {
			m.focus = paneDomains
		}
	case ActionUp:
		m.step(-1)
	case ActionDown:
		m.step(+1)
	case ActionPageUp:
		m.move(-m.listRows(m.focus))
	case ActionPageDown:
		m.move(+m.listRows(m.focus))
	case ActionSelect:
		return m.selectItem()
	case ActionShow:
		return m.showItem()
	case ActionSearch:
		m.startSearch()
	case ActionClose:
		m.clearSearch()
	case ActionEnrollKeychain:
		return m.enrollKeychain()
	case ActionRelistZones:
		return m.relistZones()
	case ActionRefresh:
		return m.refreshOrRelist()
	case ActionInsert, ActionEdit, ActionDelete:
		return m.itemAction(a)
	case ActionPreviewChanges:
		return m.previewChanges()
	case ActionApplyTemplate:
		return m.openTemplateForm()
	}

	return nil
}

// itemAction runs an insert, edit or delete Action on the focused pane's
// items: Records, Accounts or Zones. Without Accounts, insert adds one from any
// pane, as there is nothing else to insert into.
func (m *Model) itemAction(a Action) tea.Cmd {
	switch {
	case m.focus == paneAccounts, a == ActionInsert && len(m.accounts) == 0:
		return m.accountAction(a)
	case m.focus == paneRecords:
		return m.recordAction(a)
	case a == ActionInsert:
		return m.openOnboard()
	case a == ActionDelete:
		return m.openOffboard()
	}

	return m.editZone()
}

// step moves the focused pane's cursor one item. Down past the last Zone
// moves focus to the first Account below it, and Up past the first Account
// back to the current Zone; the lists otherwise stop at their ends.
func (m *Model) step(delta int) {
	switch {
	case m.focus == paneDomains && delta > 0 && m.cursor[paneDomains] >= m.itemCount(paneDomains)-1:
		m.focus = paneAccounts
		m.cursor[paneAccounts] = 0
	case m.focus == paneAccounts && delta < 0 && m.cursor[paneAccounts] == 0:
		m.focus = paneDomains
	default:
		m.move(delta)
	}
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

// selectItem opens the highlighted item of the focused pane in its edit
// dialog, as ActionEdit does.
func (m *Model) selectItem() tea.Cmd {
	switch m.focus {
	case paneDomains:
		return m.editZone()
	case paneAccounts:
		return m.accountAction(ActionEdit)
	case paneRecords:
		return m.recordAction(ActionEdit)
	}

	return nil
}

// showItem shows what the highlighted item of the focused pane holds: a
// Zone its Records, and an Account only its Zones in the Domains pane, or
// every Account's again when that Account is already the filter.
func (m *Model) showItem() tea.Cmd {
	switch m.focus {
	case paneDomains:
		return m.openZone()
	case paneAccounts:
		if len(m.accounts) == 0 {
			return nil
		}

		id := m.accounts[m.cursor[paneAccounts]].ID
		if id == m.accountFilter {
			id = 0
		}

		m.setAccount(id)
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
		m.visibleRecords = filterRecords(m.rows, m.search[paneRecords].query)
		// Taking back a pending addition removes its row.
		m.cursor[paneRecords] = min(m.cursor[paneRecords], max(len(m.visibleRecords)-1, 0))
	}
}

// rebuildRecordRows lays the open Zone's pending Changeset over its
// Records again, after either changed, and refilters the Records pane.
func (m *Model) rebuildRecordRows() {
	m.rows = m.recordRows()
	m.refilter(paneRecords)
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
	m.rebuildRecordRows()
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
