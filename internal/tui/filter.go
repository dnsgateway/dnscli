package tui

import (
	"strings"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// zoneFilter holds every criterion that decides which Zones the Domains
// pane shows. A zero zoneFilter shows every Zone.
type zoneFilter struct {
	// account limits the Zones to one Account's; zero means every Account.
	account int64
	// query is the Domains search; a Zone matches when its name contains it,
	// ignoring case. Empty means no search.
	query string
}

// match reports whether z passes every criterion of f.
func (f zoneFilter) match(z dns.Zone) bool {
	if f.account != 0 && z.AccountID != f.account {
		return false
	}

	return f.query == "" || strings.Contains(strings.ToLower(z.Name), strings.ToLower(f.query))
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

// sharedNames returns the zone names, as nameKey keys, that exist in more
// than one Account.
func sharedNames(zones []dns.Zone) map[string]bool {
	first := map[string]int64{} // zone name key -> the first Account seen with it
	shared := map[string]bool{}

	for _, z := range zones {
		k := nameKey(z.Name)
		a, seen := first[k]

		switch {
		case !seen:
			first[k] = z.AccountID
		case a != z.AccountID:
			shared[k] = true
		}
	}

	return shared
}

// nameKey normalises a zone name for comparison: DNS names ignore case, and
// a trailing dot only marks the name as fully qualified.
func nameKey(name string) string {
	return strings.TrimSuffix(strings.ToLower(name), ".")
}
