package tui

import (
	"github.com/sergelogvinov/dnscli/internal/dns"
)

// confirmZoneEdit asks to confirm the changes the Edit Zone dialog holds,
// unless one of them may not be made now: turning DNSSEC off is refused
// while unsigning would break resolution, and the dialog then says why.
// Asking to turn it off marks the Zone as being turned off this session,
// so the Zone page shows the steps towards it.
func (m *Model) confirmZoneEdit(f *zoneEditForm) {
	f.confirming, f.err = false, ""

	for _, c := range f.changes() {
		if c.setting != dnssecSetting || c.value != "off" {
			continue
		}

		m.markTurningOff(f.zone.ID)

		if why := m.unsignRefusal(f.zone); why != "" {
			f.err = why

			return
		}
	}

	f.confirming = true
}

// markTurningOff notes that the user asked to turn DNSSEC off for a Zone.
// It is held for this session only: no step is stored.
func (m *Model) markTurningOff(zoneID int64) {
	if m.turningOff == nil {
		m.turningOff = map[int64]bool{}
	}

	m.turningOff[zoneID] = true
}

// unsignRefusal says why z may not be unsigned now; empty when it may.
// While its parent serves a DS, unsigning makes validating resolvers fail
// to resolve it: the DS is removed first.
func (m *Model) unsignRefusal(z dns.Zone) string {
	if m.dsPresenceOf(z.Name) == dsServed {
		return "DNSSEC stays on: remove the DS record for " + z.Name + " at the registrar first. " +
			"dnscli watches the parent zone for it to go."
	}

	return ""
}

// turnOffStep is the next step of a signed Zone being turned off, given
// whether its parent serves a DS.
func (m *Model) turnOffStep(_ dns.Zone, ds dsPresence) dnssecStep {
	switch ds {
	case dsServed:
		return stepRemoveDS
	case dsUnknown:
		return stepUnknown
	}

	return stepSilent
}
