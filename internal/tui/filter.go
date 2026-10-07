package tui

import "github.com/sergelogvinov/dnscli/internal/dns"

// zoneFilter holds every criterion that decides which Zones the Domains
// pane shows. A zero zoneFilter shows every Zone.
type zoneFilter struct {
	// account limits the Zones to one Account's; zero means every Account.
	account int64
}

// match reports whether z passes every criterion of f.
func (f zoneFilter) match(z dns.Zone) bool {
	return f.account == 0 || z.AccountID == f.account
}

// filterZones returns the Zones that pass f, in their original order.
func filterZones(zones []dns.Zone, f zoneFilter) []dns.Zone {
	var out []dns.Zone

	for _, z := range zones {
		if f.match(z) {
			out = append(out, z)
		}
	}

	return out
}
