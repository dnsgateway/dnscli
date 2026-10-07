package tui

import (
	"strings"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// zoneStatusItems are the Zone Status of the open Zone, one item each, in
// the order the status bar shows them. Mail records come from the cached
// Records; DNSSEC and HSTS from the Zone Status the last Refresh read.
func (m *Model) zoneStatusItems(z dns.Zone) []string {
	var items []string

	if !m.recordsLoading && m.recordsErr == nil {
		items = append(items, mailItem(z.Name, m.records))
	}

	items = append(items, "DNSSEC "+z.Status.DNSSEC.String(), "HSTS "+z.Status.HSTS.String())

	// A Refresh that could not read the Zone Status left the cached one older.
	if !z.StatusAt.IsZero() && z.StatusAt.Before(z.RefreshedAt) {
		items = append(items, "settings as of "+ago(m.deps.now().Sub(z.StatusAt)))
	}

	return items
}

// renderZoneStatus renders Zone Status items for the status bar; empty
// when there are none.
func renderZoneStatus(items []string) string {
	if len(items) == 0 {
		return ""
	}

	return statusStyle.Render(strings.Join(items, " · ") + " ")
}

// mailItem says which mail records a Zone has: MX, an SPF TXT record,
// DMARC at _dmarc and DKIM keys under _domainkey.
func mailItem(zone string, records []dns.Record) string {
	var mx, spf, dmarc, dkim bool

	zone = strings.ToLower(strings.TrimSuffix(zone, "."))

	for _, r := range records {
		name := strings.ToLower(strings.TrimSuffix(r.Name, "."))
		value := strings.ToLower(strings.TrimLeft(r.Value, `"`))

		switch {
		case r.Type == "MX":
			mx = true
		case r.Type == "TXT" && name == "_dmarc."+zone && strings.HasPrefix(value, "v=dmarc1"):
			dmarc = true
		case strings.Contains(name, "._domainkey.") || strings.HasPrefix(name, "_domainkey."):
			if r.Type == "TXT" || r.Type == "CNAME" {
				dkim = true
			}
		case r.Type == "TXT" && strings.HasPrefix(value, "v=spf1"):
			spf = true
		}
	}

	var found []string

	for _, f := range []struct {
		name    string
		present bool
	}{{"MX", mx}, {"SPF", spf}, {"DMARC", dmarc}, {"DKIM", dkim}} {
		if f.present {
			found = append(found, f.name)
		}
	}

	if len(found) == 0 {
		return "mail: none"
	}

	return "mail: " + strings.Join(found, " ")
}
