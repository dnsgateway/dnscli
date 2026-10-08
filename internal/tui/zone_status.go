package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/template"
)

// Zone Status badge colors: green is configured or on, red is a setting
// that is off, blue is mail parked (records that say the Zone takes and
// sends no mail), gray is absent or unknown, and yellow is pending.
var (
	badgeOnStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	badgeOffStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	badgeParkedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	badgeAbsentStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	badgePendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

// mailState is how a Zone has one kind of mail record.
type mailState int

const (
	mailAbsent mailState = iota
	mailConfigured
	// mailParked is a record that says the Zone takes or sends no mail,
	// such as a null MX, "v=spf1 -all" or a revoked DKIM key.
	mailParked
)

// zoneStatusItems are the Zone Status of the open Zone, one rendered item
// each, in the order the status bar shows them. Mail records come from the
// cached Records; DNSSEC and HSTS from the Zone Status the last Refresh
// read; the Live Zone from the Delegation and the cached nameservers.
func (m *Model) zoneStatusItems(z dns.Zone) []string {
	var items []string

	if !m.recordsLoading && m.recordsErr == nil {
		items = append(items, mailBadges(z.Name, m.records))
	}

	items = append(items, settingBadge("DNSSEC", z.Status.DNSSEC))

	// HSTS is a Cloudflare zone setting; other Providers have none.
	if a, ok := m.account(z.AccountID); ok && a.Provider == "cloudflare" {
		items = append(items, settingBadge("HSTS", z.Status.HSTS))
	}

	// A Refresh that could not read the Zone Status left the cached one older.
	if !z.StatusAt.IsZero() && z.StatusAt.Before(z.RefreshedAt) {
		items = append(items, statusStyle.Render("settings as of "+ago(m.deps.now().Sub(z.StatusAt))))
	}

	items = append(items, statusStyle.Render(m.liveZoneText(z)))

	return items
}

// renderZoneStatus renders Zone Status items for the status bar; empty
// when there are none.
func renderZoneStatus(items []string) string {
	if len(items) == 0 {
		return ""
	}

	return strings.Join(items, statusStyle.Render(" · ")) + " "
}

// settingBadge is a Zone setting's name, green when on and red when off.
// A pending or unknown setting also says so in words.
func settingBadge(name string, s dnsprovider.Setting) string {
	switch s {
	case dnsprovider.On:
		return badgeOnStyle.Render(name)
	case dnsprovider.Off:
		return badgeOffStyle.Render(name)
	case dnsprovider.Pending:
		return badgePendingStyle.Render(name + " pending")
	}

	return badgeAbsentStyle.Render(name + " ?")
}

// mailBadges shows each kind of mail record a Zone may have, colored by
// mailState: MX and an SPF TXT record at the apex, DMARC at _dmarc and
// DKIM keys under _domainkey.
func mailBadges(zone string, records []dns.Record) string {
	mx, spf, dmarc, dkim := mailStates(zone, records)

	badges := make([]string, 0, 4)

	for _, f := range []struct {
		name  string
		state mailState
	}{{"MX", mx}, {"SPF", spf}, {"DMARC", dmarc}, {"DKIM", dkim}} {
		switch f.state {
		case mailConfigured:
			badges = append(badges, badgeOnStyle.Render(f.name))
		case mailParked:
			badges = append(badges, badgeParkedStyle.Render(f.name))
		default:
			badges = append(badges, badgeAbsentStyle.Render(f.name))
		}
	}

	return strings.Join(badges, " ")
}

// mailStates works out each kind of mail record of a Zone. A null MX
// (RFC 7505), an SPF record of only "-all" and DKIM keys that are all
// revoked (an empty p=) are parked; DMARC is parked when it rejects all
// failing mail of a Zone whose SPF is parked.
func mailStates(zone string, records []dns.Record) (mx, spf, dmarc, dkim mailState) {
	var realMX, nullMX, dmarcReject, liveKey, revokedKey bool

	zone = nameKey(zone)

	for _, r := range records {
		name := nameKey(r.Name)
		value := strings.ToLower(template.Unquote(strings.TrimSpace(r.Value)))

		switch {
		case r.Type == "MX" && name == zone && isNullMX(r.Value):
			nullMX = true
		case r.Type == "MX" && name == zone:
			realMX = true
		case r.Type == "TXT" && name == "_dmarc."+zone && strings.HasPrefix(value, "v=dmarc1"):
			dmarc = mailConfigured
			dmarcReject = tagIs(value, "p", "reject")
		case strings.Contains(name, "._domainkey.") || strings.HasPrefix(name, "_domainkey."):
			switch {
			case r.Type == "CNAME":
				liveKey = true
			case r.Type == "TXT" && tagIs(value, "p", ""):
				revokedKey = true
			case r.Type == "TXT":
				liveKey = true
			}
		case r.Type == "TXT" && name == zone && strings.HasPrefix(value, "v=spf1"):
			if strings.Join(strings.Fields(value), " ") == "v=spf1 -all" {
				spf = mailParked
			} else {
				spf = mailConfigured
			}
		}
	}

	switch {
	case realMX:
		mx = mailConfigured
	case nullMX:
		mx = mailParked
	}

	switch {
	case liveKey:
		dkim = mailConfigured
	case revokedKey:
		dkim = mailParked
	}

	if dmarc == mailConfigured && dmarcReject && spf == mailParked {
		dmarc = mailParked
	}

	return mx, spf, dmarc, dkim
}

// tagIs reports whether a tag-list value, such as "v=DMARC1; p=reject",
// has tag set to want.
func tagIs(value, tag, want string) bool {
	for t := range strings.SplitSeq(value, ";") {
		k, v, ok := strings.Cut(t, "=")
		if ok && strings.TrimSpace(k) == tag {
			return strings.TrimSpace(v) == want
		}
	}

	return false
}

// isNullMX reports whether an MX value, "<preference> <exchange>", is a
// null MX: its exchange is the root, ".".
func isNullMX(value string) bool {
	f := strings.Fields(value)

	return len(f) == 2 && f[1] == "."
}
