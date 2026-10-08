package provider

import (
	"maps"
	"slices"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// ModuleZone is a cached Zone as the module takes it: its Provider ID,
// name and nameservers. Its Hosting State is unknown to the cache.
func ModuleZone(z dns.Zone) dnsprovider.Zone {
	return dnsprovider.Zone{ID: z.ProviderID, Name: z.Name, Nameservers: slices.Clone(z.Nameservers)}
}

// CacheZone is a Zone the module listed, for the cache to sync: no
// database IDs, serial, Refresh time or Zone Status yet.
func CacheZone(z dnsprovider.Zone) dns.Zone {
	return dns.Zone{Name: z.Name, ProviderID: z.ID, Nameservers: slices.Clone(z.Nameservers)}
}

// CacheZones converts the Zones the module listed.
func CacheZones(zones []dnsprovider.Zone) []dns.Zone {
	out := make([]dns.Zone, len(zones))
	for i, z := range zones {
		out[i] = CacheZone(z)
	}

	return out
}

// ModuleRecord is a cached Record as the module takes it. The Extras, such
// as Cloudflare's proxy flag, keep their keys.
func ModuleRecord(r dns.Record) dnsprovider.Record {
	return dnsprovider.Record{ID: r.ProviderID, Type: r.Type, Name: r.Name, Value: r.Value, TTL: r.TTL, Extras: maps.Clone(r.Extras)}
}

// CacheRecord is a Record the module returned, as the cache holds it.
func CacheRecord(r dnsprovider.Record) dns.Record {
	return dns.Record{ProviderID: r.ID, Type: r.Type, Name: r.Name, Value: r.Value, TTL: r.TTL, Extras: maps.Clone(r.Extras)}
}

// CacheRecords converts the Records the module returned.
func CacheRecords(records []dnsprovider.Record) []dns.Record {
	out := make([]dns.Record, len(records))
	for i, r := range records {
		out[i] = CacheRecord(r)
	}

	return out
}

// ModuleChangeset is a pending Changeset as dnsprovider.Apply takes it.
func ModuleChangeset(cs dns.Changeset) dnsprovider.Changeset {
	out := dnsprovider.Changeset{Zone: ModuleZone(cs.Zone), Changes: make([]dnsprovider.Change, len(cs.Changes))}
	for i, c := range cs.Changes {
		out.Changes[i] = moduleChange(c)
	}

	return out
}

func moduleChange(c dns.Change) dnsprovider.Change {
	m := dnsprovider.Change{Kind: dnsprovider.ChangeKind(c.Kind)}
	if c.Kind != dns.ChangeAdd {
		m.Before = ModuleRecord(c.Before)
	}

	if c.Kind != dns.ChangeDelete {
		m.After = ModuleRecord(c.After)
	}

	return m
}

// CacheChange is a change the module reports, as the cache holds it.
func CacheChange(c dnsprovider.Change) dns.Change {
	d := dns.Change{Kind: dns.ChangeKind(c.Kind)}
	if c.Kind != dnsprovider.ChangeAdd {
		d.Before = CacheRecord(c.Before)
	}

	if c.Kind != dnsprovider.ChangeDelete {
		d.After = CacheRecord(c.After)
	}

	return d
}
