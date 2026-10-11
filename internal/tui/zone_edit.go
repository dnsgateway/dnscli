package tui

import (
	"errors"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// zoneSetting is one Zone setting the Edit Zone dialog changes. Its
// values are words, such as "on" or "1.2".
type zoneSetting struct {
	// name is the setting's name, as the dialog labels it.
	name string
	// word is the setting in a Zone Status, as the dialog shows it: one
	// of values, or a state it cannot be set to, such as "pending" or
	// "unknown".
	word func(dnsprovider.ZoneStatus) string
	// values are the values it can be set to, in the order the dialog
	// offers them.
	values []string
	// set sets this setting to v in u, leaving the others as they are.
	set func(u *dnsprovider.ZoneUpdate, v string)
	// says is setting it to v, as the dialog asks to: "turn DNSSEC on".
	says func(v string) string
	// hint says what the setting does, as its field's hint.
	hint string
	// warning is what setting it to v, from cur, does to zone beyond the
	// setting itself, given whether zone's parent serves a DS; empty when
	// nothing.
	warning func(zone, cur, v string, ds dsPresence) string
}

// settingOnOff are the values of a setting turned on or off.
var settingOnOff = []string{"on", "off"}

// The Zone settings the dialog changes, in the order it lists them.
var (
	dnssecSetting = &zoneSetting{
		name:   "DNSSEC",
		word:   func(st dnsprovider.ZoneStatus) string { return settingWord(st.DNSSEC) },
		values: settingOnOff,
		set:    func(u *dnsprovider.ZoneUpdate, v string) { on := v == "on"; u.DNSSEC = &on },
		says:   func(v string) string { return "turn DNSSEC " + v },
		hint: "Signs the Zone's Records, so validating resolvers can check them. Once on, it stays pending " +
			"until the DS record the Provider gives is added at the registrar.",
		// The warning is said only when the parent serves a DS, and,
		// hedged, while it is checked or when whether it does is unknown:
		// never taken as no DS.
		warning: func(zone, _, v string, ds dsPresence) string {
			switch {
			case v != "off" || ds == dsNone:
				return ""
			case ds == dsChecking:
				return "dnscli is checking whether the parent zone serves a DS record for " + zone + ". If it does, " +
					"remove it at the registrar first: while it is there, validating resolvers fail to resolve " +
					zone + " once it is unsigned."
			case ds == dsNotChecked, ds == dsUnknown:
				return "dnscli could not tell whether the parent zone serves a DS record for " + zone + ". If it does, " +
					"remove it at the registrar first: while it is there, validating resolvers fail to resolve " +
					zone + " once it is unsigned."
			}

			return "Remove the DS record for " + zone + " at the registrar first: while it is there, " +
				"validating resolvers fail to resolve " + zone + " once it is unsigned."
		},
	}
	hstsSetting = &zoneSetting{
		name:   "HSTS",
		word:   func(st dnsprovider.ZoneStatus) string { return settingWord(st.HSTS) },
		values: settingOnOff,
		set:    func(u *dnsprovider.ZoneUpdate, v string) { on := v == "on"; u.HSTS = &on },
		says:   func(v string) string { return "turn HSTS " + v },
		hint:   "Tells browsers to reach the zone over HTTPS only.",
		// The module turns HSTS on for six months when the Zone holds no
		// max-age, and keeps the one it holds.
		warning: func(zone, _, v string, _ dsPresence) string {
			if v != "on" {
				return ""
			}

			return "Browsers that visit " + zone + " will then use only HTTPS for it, for its HSTS max-age, " +
				"six months when it has none, even after HSTS is turned off."
		},
	}
	minTLSSetting = &zoneSetting{
		name:   "Min TLS",
		word:   func(st dnsprovider.ZoneStatus) string { return tlsWord(st.MinTLS) },
		values: tlsWords(),
		set:    func(u *dnsprovider.ZoneUpdate, v string) { t := dnsprovider.TLSVersion(v); u.MinTLS = &t },
		says:   func(v string) string { return "set the minimum TLS version to " + v },
		hint: "The oldest TLS version the Provider accepts for HTTPS to the zone. 1.2 suits most sites; " +
			"1.0 and 1.1 are deprecated.",
		// The versions compare as their words do.
		warning: func(zone, cur, v string, _ dsPresence) string {
			if v < "1.2" || (cur != "unknown" && v <= cur) {
				return ""
			}

			return "Clients that cannot use TLS " + v + " or newer, such as old browsers and devices, " +
				"will then fail to reach " + zone + " over HTTPS."
		},
	}
)

// zoneSettingsOf are the Zone settings of an Account's Provider, in the
// order the dialog lists them: DNSSEC, and the web settings of a Provider
// that has them.
func zoneSettingsOf(a dns.Account) []*zoneSetting {
	if hasWebSettings(a) {
		return []*zoneSetting{dnssecSetting, hstsSetting, minTLSSetting}
	}

	return []*zoneSetting{dnssecSetting}
}

// tlsWord says what a TLS version is: such as "1.2", or "unknown".
func tlsWord(v dnsprovider.TLSVersion) string {
	if v == dnsprovider.TLSUnknown {
		return "unknown"
	}

	return string(v)
}

// tlsWords are the TLS versions as tlsWord says them, oldest first.
func tlsWords() []string {
	out := make([]string, len(dnsprovider.TLSVersions))
	for i, v := range dnsprovider.TLSVersions {
		out[i] = string(v)
	}

	return out
}

// options are the values the setting's field offers, given the Zone
// Status: its values, after the setting as it is when it is none of them,
// such as pending, which keeps it.
func (s *zoneSetting) options(st dnsprovider.ZoneStatus) []string {
	if slices.Contains(s.values, s.word(st)) {
		return s.values
	}

	return append([]string{s.word(st)}, s.values...)
}

// zoneSettingChange is one change the Edit Zone dialog makes: a setting
// set to one of its values.
type zoneSettingChange struct {
	setting *zoneSetting
	value   string
}

// changesText says changes the way the dialog asks to make them: "turn
// DNSSEC on and set the minimum TLS version to 1.2".
func changesText(cs []zoneSettingChange) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = c.setting.says(c.value)
	}

	return strings.Join(parts, " and ")
}

// changesUpdate is the ZoneUpdate that makes every change, and sets no
// other setting.
func changesUpdate(cs []zoneSettingChange) dnsprovider.ZoneUpdate {
	var u dnsprovider.ZoneUpdate

	for _, c := range cs {
		c.setting.set(&u, c.value)
	}

	return u
}

// The rows of the Edit Zone dialog: the Account, a field per Zone setting
// from zoneFieldSettings on, then the Delegation rows and the Save button.
// Only the settings change; the other rows are shown with their hints.
const (
	zoneFieldAccount = iota
	zoneFieldSettings
)

// zoneEditForm is the Edit Zone dialog. It shows the highlighted Zone's
// settings and its Delegation, read-only (ADR-0004), in any session; its
// settings change, a Zone write, once the session is Unsealed and the
// Account's Provider is opened and can change them. Saving asks to
// confirm the changes, then makes them in one update.
type zoneEditForm struct {
	zone    dns.Zone
	account dns.Account
	// settings are the Zone settings of the Account's Provider.
	settings []*zoneSetting
	// p is the Account's Provider; nil while the settings are read-only.
	p dnsprovider.ZoneUpdater
	// readOnly says why the settings cannot be changed; empty when p is
	// set.
	readOnly string
	// unseal is set when Enter on a setting Unseals the session, to open
	// the dialog again with the settings to change.
	unseal bool
	field  int
	// choice is, for each of settings, the index of the value picked
	// among its options.
	choice []int
	// dnssecPicked is set once the user picks a DNSSEC value, rather
	// than keeping the one the Zone has.
	dnssecPicked bool
	// picker lists a setting's values; nil when closed.
	picker *picker
	// confirming is set once Save is pressed with changes, until they are
	// confirmed or Esc goes back to the form.
	confirming bool
	busy       bool // the changes are being made
	err        string
	// size is the dialog's size, which it keeps as the user moves
	// through its fields.
	size dialogSize
}

func (f *zoneEditForm) delegationField() int  { return zoneFieldSettings + len(f.settings) }
func (f *zoneEditForm) nameserversField() int { return f.delegationField() + 1 }
func (f *zoneEditForm) liveField() int        { return f.delegationField() + 2 }
func (f *zoneEditForm) saveField() int        { return f.delegationField() + 3 }

// setting is the index in settings of the setting the focused field
// changes; ok is false on any other row.
func (f *zoneEditForm) setting() (i int, ok bool) {
	i = f.field - zoneFieldSettings

	return i, i >= 0 && i < len(f.settings)
}

// options are setting i's options for the Zone as the dialog has it.
func (f *zoneEditForm) options(i int) []string {
	return f.settings[i].options(f.zone.Status)
}

// reset picks, for each setting, the value the Zone has, as cached.
func (f *zoneEditForm) reset() {
	f.choice = make([]int, len(f.settings))

	for i, s := range f.settings {
		f.choice[i] = slices.Index(f.options(i), s.word(f.zone.Status))
	}
}

// rebase takes the Zone as it is now, keeping each value picked that it
// still offers: one the Zone now has is then no change.
func (f *zoneEditForm) rebase(z dns.Zone) {
	picked := make([]string, len(f.settings))
	for i := range f.settings {
		picked[i] = f.options(i)[f.choice[i]]
	}

	f.zone = z
	f.reset()

	for i := range f.settings {
		if j := slices.Index(f.options(i), picked[i]); j >= 0 {
			f.choice[i] = j
		}
	}
}

// picked is the value s's field holds; empty when the dialog has no
// such setting.
func (f *zoneEditForm) picked(s *zoneSetting) string {
	i := slices.Index(f.settings, s)
	if i < 0 {
		return ""
	}

	return f.options(i)[f.choice[i]]
}

// pick picks option j of setting i, as the user does.
func (f *zoneEditForm) pick(i, j int) {
	f.choice[i] = j

	if f.settings[i] == dnssecSetting {
		f.dnssecPicked = true
	}
}

// change is the change setting i's field holds; ok is false when it
// keeps the setting as it is.
func (f *zoneEditForm) change(i int) (c zoneSettingChange, ok bool) {
	s := f.settings[i]
	picked := f.options(i)[f.choice[i]]

	return zoneSettingChange{setting: s, value: picked}, picked != s.word(f.zone.Status)
}

// changes are the changes the fields hold, in the order they are listed.
func (f *zoneEditForm) changes() []zoneSettingChange {
	var out []zoneSettingChange

	for i := range f.settings {
		if c, ok := f.change(i); ok {
			out = append(out, c)
		}
	}

	return out
}

// settingValue is setting i's field as the dialog shows it: the value
// picked, after the one the Zone has when they differ, as "off → on".
func (f *zoneEditForm) settingValue(i int) string {
	c, ok := f.change(i)
	if ok {
		return f.settings[i].word(f.zone.Status) + " → " + c.value
	}

	return c.value
}

// zoneEditOpenedMsg reports the Provider opened for the Edit Zone dialog;
// p is nil when it failed.
type zoneEditOpenedMsg struct {
	zone    dns.Zone
	account dns.Account
	p       dnsprovider.Provider
	err     error
	// turnDNSSEC opens the dialog asking to confirm turning DNSSEC "on"
	// or "off", the DNSSEC step taken from the Zone page; empty for none.
	turnDNSSEC string
}

// zoneSettingsSavedMsg reports changes of a Zone's settings: the Zone as
// cached with the Zone Status its Provider returned when cached is set,
// else the Zone the changes were made to.
type zoneSettingsSavedMsg struct {
	// done are the changes the Provider made; failed the ones it did not,
	// or may not have.
	done, failed []zoneSettingChange
	zone         dns.Zone
	cached       bool
	// err is why the Provider failed the update.
	err error
	// saveErr is why the returned Zone Status could not be cached, after
	// the Provider made the changes done.
	saveErr error
}

// editZone opens the Edit Zone dialog for the Zone highlighted in the
// Domains pane. In an Unsealed session, for an Account whose Provider
// type can change Zone settings, its Provider is opened first, for them
// to be changed; otherwise the dialog opens at once, the settings
// read-only, saying why.
func (m *Model) editZone() tea.Cmd {
	if len(m.visible) == 0 {
		m.notice = notice{text: "Highlight a Zone in the Domains pane first.", err: true}

		return nil
	}

	z := m.visible[m.cursor[paneDomains]]

	a, ok := m.account(z.AccountID)
	if !ok {
		return nil
	}

	// Whether the parent serves a DS decides the DNSSEC warning; it is
	// public, so it is checked whatever the session.
	checkDS := m.checkParentDS(z)

	// Whether the Provider can is known only once it is opened, which
	// needs secrets; a Provider type without Zone settings shows them
	// read-only before asking for them.
	switch {
	case !m.accountCan(a, dnsprovider.CapZoneUpdater):
		m.openZoneEdit(z, a, nil, cannotChangeZoneSettings(a), false)
	case m.key == nil:
		m.openZoneEdit(z, a, nil, "The session is sealed: Enter on a setting Unseals it, to change the settings.", true)
	default:
		return tea.Batch(checkDS, m.editZoneOf(z.ID, ""))
	}

	return checkDS
}

// cannotTurnDNSSEC says an Account's Provider cannot turn DNSSEC "on" or
// "off" through its API.
func cannotTurnDNSSEC(a dns.Account, v string) string {
	return a.Label() + " cannot turn DNSSEC " + v + " through its API: turn it " + v + " in its own panel."
}

// cannotTurnDNSSECOn says an Account's Provider cannot turn DNSSEC on
// through its API.
func cannotTurnDNSSECOn(a dns.Account) string {
	return cannotTurnDNSSEC(a, "on")
}

// pickDNSSEC picks DNSSEC v, "on" or "off", and moves to the Save button.
func (f *zoneEditForm) pickDNSSEC(v string) {
	for i, s := range f.settings {
		if s == dnssecSetting {
			f.choice[i] = slices.Index(f.options(i), v)
		}
	}

	f.field = f.saveField()
}

// cannotChangeZoneSettings says an Account's Provider cannot change Zone
// settings.
func cannotChangeZoneSettings(a dns.Account) string {
	return a.Label() + " cannot change Zone settings: its Provider has none dnscli can change."
}

// editZoneOf opens the Provider of a Zone's Account, as it is now, for
// the Edit Zone dialog: asking to confirm turning DNSSEC on or off when
// turnDNSSEC says so.
func (m *Model) editZoneOf(zoneID int64, turnDNSSEC string) tea.Cmd {
	z, ok := m.zoneByID[zoneID]
	if !ok {
		return nil
	}

	a, ok := m.account(z.AccountID)
	if !ok {
		return nil
	}

	ctx, s, key, providers := m.ctx, m.deps.Store, m.key, m.providers

	return func() tea.Msg {
		p, _, err := accountProvider(ctx, s, key, providers, a)

		return zoneEditOpenedMsg{zone: z, account: a, p: p, err: err, turnDNSSEC: turnDNSSEC}
	}
}

// tenantUse opens the Edit Zone dialog again, for the Zone as it is by
// then.
func (msg zoneEditOpenedMsg) tenantUse(m *Model) (int64, func() tea.Cmd, error) {
	return msg.account.ID, func() tea.Cmd { return m.editZoneOf(msg.zone.ID, msg.turnDNSSEC) }, msg.err
}

// openFailedText says the Provider of msg's Account could not be opened, and
// why.
func (msg zoneEditOpenedMsg) openFailedText() string {
	return "Could not open " + msg.account.Label() + ": " + msg.err.Error()
}

// zoneEditOpened opens the dialog, its settings read-only when the
// Provider could not be opened or cannot change them, unless the Tenant
// picker asks first or another dialog opened meanwhile.
func (m *Model) zoneEditOpened(msg zoneEditOpenedMsg) {
	var choice *provider.TenantChoiceError

	switch {
	case errors.As(msg.err, &choice):
		return // the Tenant picker asks, and opens the dialog again
	case m.dialogOpen() || m.tenantPick != nil:
		return
	}

	z, ok := m.zoneByID[msg.zone.ID]
	if !ok {
		return
	}

	switch up, ok := msg.p.(dnsprovider.ZoneUpdater); {
	case msg.turnDNSSEC != "" && msg.err != nil:
		m.notice = notice{text: msg.openFailedText(), err: true}
	case msg.turnDNSSEC != "" && !ok:
		m.notice = notice{text: cannotTurnDNSSEC(msg.account, msg.turnDNSSEC), err: true}
	case msg.turnDNSSEC != "":
		m.openZoneEdit(z, msg.account, up, "", false)
		m.zoneEdit.pickDNSSEC(msg.turnDNSSEC)

		if len(m.zoneEdit.changes()) > 0 {
			m.confirmZoneEdit(m.zoneEdit)
		}
	case msg.err != nil:
		m.openZoneEdit(z, msg.account, nil, msg.openFailedText(), false)
	case !ok:
		m.openZoneEdit(z, msg.account, nil, cannotChangeZoneSettings(msg.account), false)
	default:
		m.openZoneEdit(z, msg.account, up, "", false)
	}
}

// openZoneEdit opens the dialog for z, its settings changed through p, or
// read-only for the reason given when p is nil.
func (m *Model) openZoneEdit(z dns.Zone, a dns.Account, p dnsprovider.ZoneUpdater, readOnly string, unseal bool) {
	f := &zoneEditForm{
		zone:     z,
		account:  a,
		settings: zoneSettingsOf(a),
		p:        p,
		readOnly: readOnly,
		unseal:   unseal,
		field:    zoneFieldSettings,
	}
	f.reset()

	m.zoneEdit = f
	f.size = m.measureZoneEdit()
}

// zoneEditKey handles a key while the Edit Zone dialog is open. As in the
// edit-Account dialog, Up, Down and Tab move through the rows and the
// Save button, Left and Right change a setting, Enter lists its values,
// moves on from another row, or asks to confirm the changes on the Save
// button, and Enter again makes them; Esc goes back from confirming, else
// closes the dialog. While the settings are read-only, the Save button
// closes it, and Enter on a setting Unseals a sealed session.
func (m *Model) zoneEditKey(msg tea.KeyMsg) tea.Cmd {
	f := m.zoneEdit
	a := m.keys.action(msg.String())

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if f.busy {
		return nil
	}

	if f.picker != nil {
		if f.picker.key(a) {
			f.picker = nil
		}

		return nil
	}

	if f.confirming {
		switch a {
		case ActionClose:
			f.confirming = false
		case ActionSelect:
			// What allowed it may have changed while it was confirmed.
			if m.confirmZoneEdit(f); !f.confirming {
				return nil
			}

			f.busy = true

			return m.changeZoneSettings(f)
		}

		return nil
	}

	count := f.saveField() + 1
	i, onSetting := f.setting()
	changeable := onSetting && f.p != nil

	switch a {
	case ActionClose:
		m.zoneEdit = nil
	case ActionNextPane, ActionDown:
		f.field = (f.field + 1) % count
	case ActionPrevPane, ActionUp:
		f.field = (f.field + count - 1) % count
	case ActionPaneLeft, ActionPaneRight:
		if changeable {
			n := len(f.options(i))

			step := 1
			if a == ActionPaneLeft {
				step = n - 1
			}

			f.pick(i, (f.choice[i]+step)%n)
		}
	case ActionSelect:
		if f.field == f.saveField() && f.p != nil {
			m.cancelTurningOff(f)
		}

		switch {
		case f.field == f.saveField() && (f.p == nil || len(f.changes()) == 0):
			// Like an Account saved unchanged, nothing to make closes it.
			m.zoneEdit = nil
		case f.field == f.saveField():
			m.confirmZoneEdit(f)
		case changeable:
			f.picker = &picker{
				title:   f.settings[i].name,
				options: f.options(i),
				cursor:  f.choice[i],
				pick:    func(j int) { f.pick(i, j) },
			}
		case onSetting && f.unseal:
			// The dialog opens again, its settings to change, once the
			// session is Unsealed and the Provider opened.
			m.zoneEdit = nil
			id := f.zone.ID

			return m.withSecrets(func() tea.Cmd { return m.editZoneOf(id, "") })
		default:
			f.field++
		}
	}

	return nil
}

// changeZoneSettings makes the changes picked, in one update, and caches
// the settings the Provider made, as its Zone Status has them, written
// now. When the update fails, a change the Provider returns known was
// made before the failure; one it returns unknown may not have been, and
// keeps its cached value.
func (m *Model) changeZoneSettings(f *zoneEditForm) tea.Cmd {
	ctx, s, now := m.ctx, m.deps.Store, m.deps.now
	z, p, cs := f.zone, f.p, f.changes()

	return func() tea.Msg {
		st, err := p.UpdateZone(ctx, provider.ModuleZone(z), changesUpdate(cs))

		var done, failed []zoneSettingChange

		for _, c := range cs {
			if err == nil || c.setting.word(st) != "unknown" {
				done = append(done, c)
			} else {
				failed = append(failed, c)
			}
		}

		if len(done) == 0 {
			return zoneSettingsSavedMsg{failed: failed, zone: z, err: err}
		}

		saved, saveErr := s.SaveZoneStatus(ctx, z, changesUpdate(done), st, now())
		if saveErr != nil {
			return zoneSettingsSavedMsg{done: done, failed: failed, zone: z, err: err, saveErr: saveErr}
		}

		return zoneSettingsSavedMsg{done: done, failed: failed, zone: saved, cached: true, err: err}
	}
}

// zoneSettingsSaved reports changes of a Zone's settings. When the update
// failed, the dialog stays open with the error, naming the changes made
// and the ones that failed, its fields as the Zone is cached now with
// the other values picked kept; made changes, or ones that could not be
// cached, close it. The Zone Status cached is updated wherever the
// session holds the Zone.
func (m *Model) zoneSettingsSaved(msg zoneSettingsSavedMsg) tea.Cmd {
	f := m.zoneEdit
	if f != nil && f.zone.ID != msg.zone.ID {
		f = nil
	}

	// Turning DNSSEC on or off changes the keys its nameservers serve.
	var keys tea.Cmd

	if msg.cached {
		before := m.zoneByID[msg.zone.ID]
		m.updateZone(msg.zone)
		keys = m.signingKeysChanged(before)
	}

	for _, c := range msg.done {
		if c.setting == dnssecSetting {
			m.dnssecTurned(msg.zone.ID, c.value)
		}
	}

	switch {
	case msg.saveErr != nil:
		if f != nil {
			m.zoneEdit = nil
		}

		text := capitalise(changesText(msg.done)) + " for " + msg.zone.Name + " was done, but could not be cached: " + msg.saveErr.Error()
		if msg.err != nil {
			text += "; " + failedText(msg.failed, msg.err)
		}

		m.notice = notice{text: text, err: true}
	case msg.err != nil:
		if f != nil {
			f.rebase(msg.zone)
			f.busy, f.confirming = false, false
			f.err = failedText(msg.failed, msg.err)

			if len(msg.done) > 0 {
				f.err = capitalise(changesText(msg.done)) + " was done; " + f.err
			}

			f.err = capitalise(f.err)
		}
	default:
		if f != nil {
			m.zoneEdit = nil
		}

		said := make([]string, len(msg.done))
		for i, c := range msg.done {
			said[i] = c.setting.name + " of " + msg.zone.Name + " is " + c.setting.word(msg.zone.Status)
		}

		m.notice = notice{text: strings.Join(said, "; ") + "."}
	}

	return keys
}

// failedText says why an update failed: the changes it did not make, or,
// when it made every one, that the Provider failed after them.
func failedText(failed []zoneSettingChange, err error) string {
	if len(failed) == 0 {
		return "the Provider then failed: " + err.Error()
	}

	return "could not " + changesText(failed) + ": " + err.Error()
}

// settingWord says what a setting is: "on", "off", "pending" or "unknown".
func settingWord(s dnsprovider.Setting) string {
	if s == dnsprovider.Unknown {
		return "unknown"
	}

	return s.String()
}

// zoneDelegation is the Delegation of z's zone name as the dialog shows
// it: the delegated nameservers, or how far the lookup got.
func (m *Model) zoneDelegation(z dns.Zone) []string {
	d, ok := m.delegations[nameKey(z.Name)]

	switch {
	case m.deps.Resolver == nil || !ok:
		return []string{"not looked up"}
	case d.state == lookupDone:
		return d.ns
	case d.state == lookupFailed:
		return []string{"lookup failed"}
	}

	return []string{"resolving…"}
}

// listField is a fixed dialog row showing values one per row.
func listField(label string, values []string) dialogField {
	return dialogField{label: label, value: values[0], more: values[1:], fixed: true}
}

// liveWord says in a word or two whether z is the Live Zone.
func (m *Model) liveWord(z dns.Zone) string {
	switch m.liveness(z) {
	case liveYes:
		return liveStyle.Render("● yes")
	case liveNo:
		return "no"
	}

	if m.checkingLive(z) {
		return "checking…"
	}

	return "unknown"
}

// zoneEditFields are the rows of the dialog, with each setting's value as
// value(i); fixed while the settings are read-only.
func (m *Model) zoneEditFields(f *zoneEditForm, value func(i int) string) []dialogField {
	fields := []dialogField{{label: "Account", value: f.account.Label(), fixed: true}}

	for i, s := range f.settings {
		fields = append(fields, dialogField{label: s.name, value: value(i), choice: f.p != nil, fixed: f.p == nil})
	}

	ns := f.zone.Nameservers
	if len(ns) == 0 {
		ns = []string{"not cached"}
	}

	return append(fields,
		listField("Delegation", m.zoneDelegation(f.zone)),
		listField("Nameservers", ns),
		dialogField{label: "Live Zone", value: m.liveWord(f.zone), fixed: true},
	)
}

// zoneEditHints are the lines at the bottom of the dialog with field
// focused: what the row is for, and for a setting, what the value picked,
// warn, would do beyond it, or why it cannot be changed.
func (m *Model) zoneEditHints(f *zoneEditForm, field int, warn string) []string {
	z := f.zone

	var hint string

	i := field - zoneFieldSettings
	onSetting := i >= 0 && i < len(f.settings)

	switch {
	case field == zoneFieldAccount:
		hint = "The Account hosting the Zone. A Zone stays in its Account: Onboard the zone name in another to host it there."
	case onSetting:
		hint = f.settings[i].hint
	case field == f.delegationField():
		hint = "The nameservers the parent zone delegates " + z.Name + " to, as the system resolver gives them"
		if d, ok := m.delegations[nameKey(z.Name)]; ok && d.state == lookupFailed {
			hint += "; the lookup failed: " + d.err.Error()
		}

		hint += ". dnscli does not change Delegation (ADR-0004)."
	case field == f.nameserversField():
		hint = "The nameservers " + f.account.Label() + " expects for the Zone."
		if len(z.Nameservers) == 0 {
			hint = "The nameservers " + f.account.Label() + " expects for the Zone are not cached: re-list the Account (" +
				m.keys.keysFor(ActionRelistZones) + ") to read them."
		}
	case field == f.liveField():
		hint = "A Zone is the Live Zone when its nameservers are the delegated ones: its Records are the ones that answer."
	case f.p == nil:
		hint = "Closes the dialog."
	default:
		hint = "Asks to confirm, then changes the settings at " + f.account.Label() + "; the other rows are read-only."
	}

	hints := wrapNote(hintStyle, hint, noteWidth)

	if f.readOnly != "" && (onSetting || field == f.saveField()) {
		hints = append(hints, "")
		hints = append(hints, wrapNote(hintStyle, f.readOnly, noteWidth)...)
	}

	if warn != "" {
		hints = append(hints, "")
		hints = append(hints, wrapNote(errorStyle, warn, noteWidth)...)
	}

	return hints
}

// warning is what the change the focused setting field holds would do
// beyond the setting, given whether the parent serves a DS; empty when
// nothing.
func (f *zoneEditForm) warning(ds dsPresence) string {
	i, ok := f.setting()
	if !ok {
		return ""
	}

	c, ok := f.change(i)
	if !ok {
		return ""
	}

	return c.setting.warning(f.zone.Name, c.setting.word(f.zone.Status), c.value, ds)
}

// zoneEditKeys are the keys the Edit Zone dialog takes, as the dialog and
// the status line say them.
func (m *Model) zoneEditKeys() string {
	f, k := m.zoneEdit, m.keys.keysFor

	switch {
	case f.busy:
		return "changing…"
	case f.confirming:
		return k(ActionSelect) + " apply · " + k(ActionClose) + " back"
	case f.p == nil:
		return k(ActionUp) + " " + k(ActionDown) + " field · " + k(ActionClose) + " close"
	}

	return m.formKeysHint(true)
}

func (m *Model) zoneEditView() string {
	f := m.zoneEdit
	z := f.zone

	var notes, footer []string

	switch {
	case f.confirming || f.busy:
		// The changes to apply and their warnings take the hints' place,
		// centered at the bottom of the dialog with the buttons.
		cs := f.changes()
		footer = wrapNote(lipgloss.NewStyle(), capitalise(changesText(cs))+" for "+z.Name+" at "+f.account.Label()+"?", noteWidth)

		for _, c := range cs {
			if w := c.setting.warning(z.Name, c.setting.word(z.Status), c.value, m.dsPresenceOf(z.Name)); w != "" {
				footer = append(footer, "")
				footer = append(footer, wrapNote(errorStyle, w, noteWidth)...)
			}
		}

		// The Save button becomes the Apply button, focused: Enter
		// presses it, Esc goes back to the form.
		footer = append(footer, "", hintStyle.Render(m.zoneEditKeys()), "", applyRow())
	default:
		footer = append(m.zoneEditHints(f, f.field, f.warning(m.dsPresenceOf(z.Name))), "", f.buttonRow(f.field == f.saveField()))
	}

	if f.err != "" {
		notes = wrapNote(errorStyle, f.err, noteWidth)
	}

	return sizedDialogView("Edit Zone "+z.Name, m.zoneEditFields(f, f.settingValue), f.field, false, notes, footer, f.size)
}

// buttonRow renders the dialog's button: Save, or Close while the
// settings are read-only; marked when focused.
func (f *zoneEditForm) buttonRow(focused bool) string {
	if f.p != nil {
		return saveRow(focused)
	}

	if focused {
		return "› " + cursorStyle.Render("[ Close ]")
	}

	return "  [ Close ]"
}

// applyRow renders the Apply button the Save button becomes once the
// changes are to be confirmed: focused, as saveRow renders it.
func applyRow() string {
	return "› " + cursorStyle.Render("[ Apply ]")
}

// measureZoneEdit works out the largest size the dialog takes, over every
// focused row and every value a setting may be changed to, with the other
// rows as wide as the dialog shows them, two nameservers each. The dialog
// then keeps that size; only more nameservers, an error or the
// confirmation grow it.
func (m *Model) measureZoneEdit() dialogSize {
	f := m.zoneEdit
	widest := strings.Repeat("W", maxValueWidth)

	fields := m.zoneEditFields(f, func(int) string { return "unknown → off" })
	fields[f.delegationField()] = listField("Delegation", []string{widest, widest})
	fields[f.nameserversField()] = listField("Nameservers", []string{widest, widest})

	var size dialogSize

	for field := range f.saveField() + 1 {
		warnings := []string{""}
		if i := field - zoneFieldSettings; i >= 0 && i < len(f.settings) && f.p != nil {
			for _, v := range f.settings[i].values {
				for _, ds := range []dsPresence{dsServed, dsChecking, dsUnknown} {
					warnings = append(warnings, f.settings[i].warning(f.zone.Name, "unknown", v, ds))
				}
			}
		}

		for _, w := range warnings {
			footer := append(m.zoneEditHints(f, field, w), "", f.buttonRow(field == f.saveField()))
			size = size.grow(sizedDialogView("Edit Zone "+f.zone.Name, fields, field, false, nil, footer, dialogSize{}))
		}
	}

	return size
}
