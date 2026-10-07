// Package cloudflare is the Cloudflare Provider, built on cloudflare-go
// (ADR-0003).
package cloudflare

import (
	"context"
	"errors"
	"net/http"
	"strconv"

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
	all, err := listAll(ctx, zonesPerPage, func(opts ...option.RequestOption) (*pagination.V4PagePaginationArray[zones.Zone], error) {
		return p.zones.List(ctx, zones.ZoneListParams{}, opts...)
	})
	if err != nil {
		return nil, err
	}

	out := make([]dns.Zone, 0, len(all))
	for _, z := range all {
		out = append(out, dns.Zone{Name: z.Name, ProviderID: z.ID})
	}

	return out, nil
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
	return provider.RecordSet{}, errors.New("todo")
}

// Apply implements provider.Provider.
func (p *Provider) Apply(ctx context.Context, cs dns.Changeset) (provider.ApplyResult, error) {
	return provider.ApplyResult{}, errors.New("todo")
}

// ZoneStatus implements provider.Provider.
func (p *Provider) ZoneStatus(ctx context.Context, zone dns.Zone) (dns.ZoneStatus, error) {
	return dns.ZoneStatus{}, errors.New("todo")
}

// Nameservers implements provider.Provider.
func (p *Provider) Nameservers(ctx context.Context) ([]string, error) {
	return nil, errors.New("todo")
}
