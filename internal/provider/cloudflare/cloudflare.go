// Package cloudflare is the Cloudflare Provider, built on cloudflare-go
// (ADR-0003).
package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	cfdns "github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/cloudflare/cloudflare-go/v7/packages/pagination"
	"github.com/cloudflare/cloudflare-go/v7/zones"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// DefaultBaseURL is the Cloudflare v4 API.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4/"

// zonesPerPage is the largest page Cloudflare serves for Zones.
const zonesPerPage = 50

// recordsPerPage keeps a Refresh of a large Zone to a few requests.
const recordsPerPage = 1000

// Config configures the Cloudflare Provider. Its zero value talks to the
// real Cloudflare API.
type Config struct {
	// BaseURL is the API base URL; empty means DefaultBaseURL.
	BaseURL string
	// HTTPClient sends the requests; nil means a default client.
	HTTPClient *http.Client
}

// New is the provider.Constructor for Cloudflare. The token is a
// Cloudflare API token.
//
// It ignores the CLOUDFLARE_* environment variables cloudflare-go reads by
// default: an Account's credentials come only from the Account.
func (c Config) New(_ dns.Account, token string) (provider.Provider, error) {
	if token == "" {
		return nil, errors.New("cloudflare: no API token")
	}

	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	opts := []option.RequestOption{
		option.WithBaseURL(baseURL),
		option.WithAPIToken(token),
	}
	if c.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(c.HTTPClient))
	}

	return &Provider{
		zones:   zones.NewZoneService(opts...),
		records: cfdns.NewRecordService(opts...),
	}, nil
}

// Provider is one Cloudflare Account.
type Provider struct {
	zones   *zones.ZoneService
	records *cfdns.RecordService
}

var _ provider.Provider = (*Provider)(nil)

// Zones implements provider.Provider.
func (p *Provider) Zones(ctx context.Context) ([]dns.Zone, error) {
	all, err := p.listZones(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]dns.Zone, 0, len(all))
	for _, z := range all {
		out = append(out, dns.Zone{Name: z.Name, ProviderID: z.ID})
	}

	return out, nil
}

func (p *Provider) listZones(ctx context.Context) ([]zones.Zone, error) {
	return listAll(ctx, zonesPerPage, func(opts ...option.RequestOption) (*pagination.V4PagePaginationArray[zones.Zone], error) {
		return p.zones.List(ctx, zones.ZoneListParams{}, opts...)
	})
}

// listAll reads every page of a v4 list. It pages by hand, rather than with
// cloudflare-go's auto-pager, so that later pages keep the caller's context
// and paging stops at result_info.total_pages.
func listAll[T any](ctx context.Context, perPage int, list func(...option.RequestOption) (*pagination.V4PagePaginationArray[T], error)) ([]T, error) {
	var out []T

	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		res, err := list(
			option.WithQuery("page", strconv.Itoa(page)),
			option.WithQuery("per_page", strconv.Itoa(perPage)),
		)
		if err != nil {
			return nil, err
		}

		out = append(out, res.Result...)

		// cloudflare-go does not decode total_pages.
		total, err := strconv.Atoi(res.ResultInfo.JSON.ExtraFields["total_pages"].Raw())
		if err != nil {
			total = page
			if len(res.Result) > 0 && int64(len(res.Result)) >= res.ResultInfo.PerPage {
				total = page + 1
			}
		}

		if page >= total || len(res.Result) == 0 {
			return out, nil
		}
	}
}

// Records implements provider.Provider.
func (p *Provider) Records(ctx context.Context, zone dns.Zone) (provider.RecordSet, error) {
	// ZoneID is a param.Field, which only cloudflare-go's root package can
	// build with F(); importing that package compiles the whole SDK.
	var params cfdns.RecordListParams
	params.ZoneID.Value, params.ZoneID.Present = zone.ProviderID, true

	all, err := listAll(ctx, recordsPerPage, func(opts ...option.RequestOption) (*pagination.V4PagePaginationArray[cfdns.RecordResponse], error) {
		return p.records.List(ctx, params, opts...)
	})
	if err != nil {
		return provider.RecordSet{}, err
	}

	records := make([]dns.Record, 0, len(all))
	for _, r := range all {
		rec, err := toRecord(r)
		if err != nil {
			return provider.RecordSet{}, err
		}

		records = append(records, rec)
	}

	serial, err := p.serial(ctx, zone)
	if err != nil {
		return provider.RecordSet{}, err
	}

	return provider.RecordSet{Records: records, Serial: serial}, nil
}

// serial reads the Zone's SOA serial. The v4 API lists no SOA record, so
// it comes from the SOA line of the Zone's BIND export; zero when the
// export has none.
func (p *Provider) serial(ctx context.Context, zone dns.Zone) (uint32, error) {
	var params cfdns.RecordExportParams
	params.ZoneID.Value, params.ZoneID.Present = zone.ProviderID, true

	export, err := p.records.Export(ctx, params)
	if err != nil {
		return 0, err
	}

	if export == nil {
		return 0, nil
	}

	return soaSerial(*export), nil
}

// soaSerial finds the serial in a zone file's SOA record:
// <name> [ttl] [class] SOA <mname> <rname> <serial> ...
func soaSerial(zoneFile string) uint32 {
	for line := range strings.Lines(zoneFile) {
		line, _, _ = strings.Cut(line, ";")
		fields := strings.Fields(line)

		i := slices.IndexFunc(fields, func(f string) bool { return strings.EqualFold(f, "SOA") })
		if i < 0 || i+3 >= len(fields) {
			continue
		}

		serial, err := strconv.ParseUint(fields[i+3], 10, 32)
		if err != nil {
			continue
		}

		return uint32(serial)
	}

	return 0
}

// proxiedExtra is the Record extra holding Cloudflare's proxy flag, "true"
// or "false". It is set only on Records Cloudflare can proxy.
const proxiedExtra = "proxied"

// wireRecord is the part of a v4 DNS record that dnscli reads.
//
// It is decoded from the record's raw JSON: cloudflare-go decodes records
// into an untagged union that settles on the A-record shape for every type,
// dropping type-specific fields such as the MX priority.
type wireRecord struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Name      string  `json:"name"`
	Content   string  `json:"content"`
	TTL       uint32  `json:"ttl"`
	Priority  float64 `json:"priority"`
	Proxiable bool    `json:"proxiable"`
	Proxied   bool    `json:"proxied"`
}

func toRecord(r cfdns.RecordResponse) (dns.Record, error) {
	var w wireRecord
	if err := json.Unmarshal([]byte(r.JSON.RawJSON()), &w); err != nil {
		return dns.Record{}, fmt.Errorf("cloudflare: decode record %s: %w", r.ID, err)
	}

	rec := dns.Record{
		ProviderID: w.ID,
		Type:       w.Type,
		Name:       w.Name,
		Value:      w.Content,
		TTL:        w.TTL,
	}

	// Cloudflare keeps the MX and SRV priority out of the content; the
	// Value is in zone-file presentation form, priority first.
	if w.Type == "MX" || w.Type == "SRV" {
		rec.Value = strconv.FormatFloat(w.Priority, 'f', -1, 64) + " " + w.Content
	}

	if w.Proxiable {
		rec.Extras = map[string]string{proxiedExtra: strconv.FormatBool(w.Proxied)}
	}

	return rec, nil
}

// Apply implements provider.Provider.
func (p *Provider) Apply(ctx context.Context, cs dns.Changeset) (provider.ApplyResult, error) {
	return provider.ApplyResult{}, errors.New("todo")
}

// ZoneStatus implements provider.Provider.
func (p *Provider) ZoneStatus(ctx context.Context, zone dns.Zone) (dns.ZoneStatus, error) {
	return dns.ZoneStatus{}, errors.New("todo")
}

// Nameservers implements provider.Provider. Cloudflare assigns nameservers
// per Zone, usually the same pair across an Account, so the Account
// expects every nameserver assigned to any of its Zones, sorted.
func (p *Provider) Nameservers(ctx context.Context) ([]string, error) {
	all, err := p.listZones(ctx)
	if err != nil {
		return nil, err
	}

	var ns []string
	for _, z := range all {
		ns = append(ns, z.NameServers...)
	}

	slices.Sort(ns)

	return slices.Compact(ns), nil
}
