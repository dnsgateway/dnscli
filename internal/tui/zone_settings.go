package tui

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// zoneSettingChange is one change the Zone settings dialog offers: a
// setting turned on or off.
type zoneSettingChange struct {
	// name is the setting's name, "DNSSEC" or "HSTS".
	name string
	on   bool
}

// text is the change as the dialog lists it: "Turn DNSSEC on".
func (c zoneSettingChange) text() string {
	return "Turn " + c.name + " " + onOrOff(c.on)
}

// update is the change as a ZoneUpdate, which sets this setting alone.
func (c zoneSettingChange) update() dnsprovider.ZoneUpdate {
	on := c.on
	if c.name == "DNSSEC" {
		return dnsprovider.ZoneUpdate{DNSSEC: &on}
	}

	return dnsprovider.ZoneUpdate{HSTS: &on}
}

// onOrOff is "on" or "off".
func onOrOff(on bool) string {
	if on {
		return "on"
	}

	return "off"
}

// zoneSettings is the dialog that turns the selected Zone's DNSSEC or
// HSTS on or off, a Zone write: it lists the changes the cached Zone
// Status allows, and the one picked is made only once confirmed.
type zoneSettings struct {
	zone    dns.Zone
	account dns.Account
	// p is the Account's Provider, a dnsprovider.ZoneUpdater.
	p       dnsprovider.ZoneUpdater
	changes []zoneSettingChange
	cursor  int
	// confirming is set once a change is picked, until it is confirmed
	// or Esc goes back to the list.
	confirming bool
	busy       bool // the change is being made
	err        string
}

// zoneSettingsOpenedMsg reports the Provider opened for the Zone settings
// dialog; p is nil when it failed.
type zoneSettingsOpenedMsg struct {
	zone    dns.Zone
	account dns.Account
	p       dnsprovider.Provider
	err     error
}

// zoneSettingsSavedMsg reports a change of a Zone's settings: the Zone as
// cached with the Zone Status its Provider returned.
type zoneSettingsSavedMsg struct {
	change zoneSettingChange
	zone   dns.Zone
	err    error
	// saveErr is why the returned Zone Status could not be cached, after
	// the Provider made the change.
	saveErr error
}

// openZoneSettings opens the Zone settings dialog for the Zone highlighted
// in the Domains pane. Changing them is a Zone write: a sealed session
// Unseals first, and the Account's Provider must be able to change them.
func (m *Model) openZoneSettings() tea.Cmd {
	if m.focus != paneDomains || len(m.visible) == 0 {
		m.notice = notice{text: "Highlight a Zone in the Domains pane first.", err: true}

		return nil
	}

	z := m.visible[m.cursor[paneDomains]]

	return m.withSecrets(func() tea.Cmd { return m.openZoneSettingsOf(z.ID) })
}

// openZoneSettingsOf opens the Provider of a Zone's Account, as it is
// now, for the Zone settings dialog.
func (m *Model) openZoneSettingsOf(zoneID int64) tea.Cmd {
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

		return zoneSettingsOpenedMsg{zone: z, account: a, p: p, err: err}
	}
}

// tenantUse opens the Zone settings dialog again, for the Zone as it is
// by then.
func (msg zoneSettingsOpenedMsg) tenantUse(m *Model) (int64, func() tea.Cmd, error) {
	return msg.account.ID, func() tea.Cmd { return m.openZoneSettingsOf(msg.zone.ID) }, msg.err
}

// zoneSettingsOpened opens the dialog, unless the Provider could not be
// opened or cannot change Zone settings, or another dialog opened
// meanwhile.
func (m *Model) zoneSettingsOpened(msg zoneSettingsOpenedMsg) {
	var choice *provider.TenantChoiceError

	switch {
	case errors.As(msg.err, &choice):
		return // the Tenant picker asks, and opens the dialog again
	case msg.err != nil:
		m.notice = notice{text: "Could not open " + msg.account.Label() + ": " + msg.err.Error(), err: true}

		return
	case !dnsprovider.CanUpdateZone(msg.p):
		m.notice = notice{text: msg.account.Label() + " cannot change Zone settings: its Provider has none dnscli can change.", err: true}

		return
	case m.dialogOpen() || m.tenantPick != nil || m.zoneSettings != nil:
		return
	}

	z, ok := m.zoneByID[msg.zone.ID]
	if !ok {
		return
	}

	m.zoneSettings = &zoneSettings{
		zone:    z,
		account: msg.account,
		p:       msg.p.(dnsprovider.ZoneUpdater),
		changes: settingChanges(z.Status),
	}
}

// settingChanges are the changes a Zone Status allows: a setting that is
// on can be turned off, one that is off turned on, and one pending or
// unknown either way.
func settingChanges(st dnsprovider.ZoneStatus) []zoneSettingChange {
	var out []zoneSettingChange

	for _, s := range []struct {
		name    string
		setting dnsprovider.Setting
	}{{"DNSSEC", st.DNSSEC}, {"HSTS", st.HSTS}} {
		if s.setting != dnsprovider.On {
			out = append(out, zoneSettingChange{name: s.name, on: true})
		}

		if s.setting != dnsprovider.Off {
			out = append(out, zoneSettingChange{name: s.name, on: false})
		}
	}

	return out
}

// zoneSettingsKey handles a key while the Zone settings dialog is open:
// Up and Down pick a change, Enter asks to confirm it and Enter again
// makes it; Esc goes back from confirming, else closes the dialog.
func (m *Model) zoneSettingsKey(msg tea.KeyMsg) tea.Cmd {
	d := m.zoneSettings
	a := m.keys.action(msg.String())

	if quitsDialog(a, msg) {
		return m.handle(ActionQuit)
	}

	if d.busy {
		return nil
	}

	switch a {
	case ActionUp:
		if !d.confirming {
			d.cursor = max(d.cursor-1, 0)
		}
	case ActionDown:
		if !d.confirming {
			d.cursor = min(d.cursor+1, len(d.changes)-1)
		}
	case ActionClose:
		if d.confirming {
			d.confirming = false
		} else {
			m.zoneSettings = nil
		}
	case ActionSelect:
		if !d.confirming {
			d.confirming = true
			d.err = ""

			return nil
		}

		d.busy = true

		return m.changeZoneSetting(d)
	}

	return nil
}

// changeZoneSetting makes the change picked, and caches the Zone Status
// the Provider returns, read now.
func (m *Model) changeZoneSetting(d *zoneSettings) tea.Cmd {
	ctx, s, now := m.ctx, m.deps.Store, m.deps.now
	z, p, c := d.zone, d.p, d.changes[d.cursor]

	return func() tea.Msg {
		st, err := p.UpdateZone(ctx, provider.ModuleZone(z), c.update())
		if err != nil {
			return zoneSettingsSavedMsg{change: c, zone: z, err: err}
		}

		saved, err := s.SaveZoneStatus(ctx, z, st, now())

		return zoneSettingsSavedMsg{change: c, zone: saved, saveErr: err}
	}
}

// zoneSettingsSaved reports a change of a Zone's settings. A change the
// Provider refused leaves the dialog open with the error; a made one
// closes it and updates the Zone wherever the session holds it.
func (m *Model) zoneSettingsSaved(msg zoneSettingsSavedMsg) {
	d := m.zoneSettings

	if msg.err != nil {
		if d != nil && d.zone.ID == msg.zone.ID {
			d.busy, d.confirming = false, false
			d.err = "Could not turn " + msg.change.name + " " + onOrOff(msg.change.on) + ": " + msg.err.Error()
		}

		return
	}

	if d != nil && d.zone.ID == msg.zone.ID {
		m.zoneSettings = nil
	}

	if msg.saveErr != nil {
		m.notice = notice{text: msg.change.text() + " for " + msg.zone.Name + " was done, but could not be cached: " + msg.saveErr.Error(), err: true}

		return
	}

	m.updateZone(msg.zone)

	var st dnsprovider.Setting
	if msg.change.name == "DNSSEC" {
		st = msg.zone.Status.DNSSEC
	} else {
		st = msg.zone.Status.HSTS
	}

	m.notice = notice{text: msg.change.name + " of " + msg.zone.Name + " is " + settingWord(st) + "."}
}

// settingWord says what a setting is: "on", "off", "pending" or "unknown".
func settingWord(s dnsprovider.Setting) string {
	if s == dnsprovider.Unknown {
		return "unknown"
	}

	return s.String()
}

func (m *Model) zoneSettingsView() string {
	d := m.zoneSettings
	z := d.zone

	notes := []string{"DNSSEC " + settingWord(z.Status.DNSSEC) + " · HSTS " + settingWord(z.Status.HSTS), ""}

	if d.confirming {
		c := d.changes[d.cursor]
		notes = append(notes, wrapNote(lipgloss.NewStyle(), c.text()+" for "+z.Name+" at "+d.account.Label()+"?", noteWidth)...)
	} else {
		for i, c := range d.changes {
			line := "  " + c.text()
			if i == d.cursor {
				line = cursorStyle.Render("› " + c.text())
			}

			notes = append(notes, line)
		}
	}

	if d.err != "" {
		notes = append(notes, "")
		notes = append(notes, wrapNote(errorStyle, d.err, noteWidth)...)
	}

	var hint string

	switch {
	case d.busy:
		hint = "changing…"
	case d.confirming:
		hint = m.keys.keysFor(ActionSelect) + " confirm · " + m.keys.keysFor(ActionClose) + " back"
	default:
		hint = m.keys.keysFor(ActionSelect) + " choose · " + m.keys.keysFor(ActionClose) + " close"
	}

	notes = append(notes, "", hintStyle.Render(hint))

	return dialogView("Zone settings of "+z.Name, nil, 0, false, notes)
}
