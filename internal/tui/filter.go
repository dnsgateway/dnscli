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

// filterZones returns the Zones that pass f, in their original order.
func filterZones(zones []dns.Zone, f zoneFilter) []dns.Zone {
	q := strings.ToLower(f.query)

	var out []dns.Zone

	for _, z := range zones {
		if f.account != 0 && z.AccountID != f.account {
			continue
		}

		if q == "" || strings.Contains(strings.ToLower(z.Name), q) {
			out = append(out, z)
		}
	}

	return out
}

// filterRecords returns the Records whose name, type or value contains
// query, ignoring case, in their original order. An empty query matches
// every Record.
func filterRecords(records []dns.Record, query string) []dns.Record {
	q := strings.ToLower(query)
	if q == "" {
		return records
	}

	var out []dns.Record

	for _, r := range records {
		for _, field := range []string{r.Name, r.Type, r.Value} {
			if strings.Contains(strings.ToLower(field), q) {
				out = append(out, r)

				break
			}
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
