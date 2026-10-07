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
	"sync"
	"time"

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
	// HTTPClient sends the requests; nil means a client with connection
	// timeouts, so a stalled connection cannot hang a call.
	HTTPClient *http.Client
	// RequestTimeout bounds each HTTP request, including reading its
	// response; zero means DefaultRequestTimeout.
	RequestTimeout time.Duration
}

// DefaultRequestTimeout bounds each request to the Cloudflare API. A
// failed request may be retried, each try with its own timeout.
const DefaultRequestTimeout = 30 * time.Second

// defaultHTTPClient is http.DefaultTransport with the timeouts
// cloudflare-go's own default client sets, which building the services
// from bare options leaves out.
func defaultHTTPClient() *http.Client {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{}
	}

	t = t.Clone()
	t.ResponseHeaderTimeout = DefaultRequestTimeout

	return &http.Client{Transport: t}
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

	client := c.HTTPClient
	if client == nil {
		client = defaultHTTPClient()
	}

	timeout := c.RequestTimeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}

	opts := []option.RequestOption{
		option.WithBaseURL(baseURL),
		option.WithAPIToken(token),
		option.WithMiddleware(reportRateLimits),
		option.WithHTTPClient(client),
		option.WithRequestTimeout(timeout),
	}

	return &Provider{
		zones:    zones.NewZoneService(opts...),
		settings: zones.NewSettingService(opts...),
		records:  cfdns.NewRecordService(opts...),
		dnssec:   cfdns.NewDNSSECService(opts...),
	}, nil
}

// Provider is one Cloudflare Account.
type Provider struct {
	zones    *zones.ZoneService
	settings *zones.SettingService
	records  *cfdns.RecordService
	dnssec   *cfdns.DNSSECService
}

var (
	_ provider.Provider  = (*Provider)(nil)
	_ provider.TTLLimits = (*Provider)(nil)
)

// TTLRange implements provider.TTLLimits: below Enterprise plans, a
// Cloudflare TTL is at least 60 seconds, besides 1 for automatic.
func (p *Provider) TTLRange() (minTTL, maxTTL uint32) {
	return 60, 86400
}

// Zones implements provider.Provider. Each Zone carries the nameservers
// Cloudflare assigned to it, which can differ between Zones of one Account.
func (p *Provider) Zones(ctx context.Context) ([]dns.Zone, error) {
	all, err := p.listZones(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]dns.Zone, 0, len(all))
	for _, z := range all {
		out = append(out, dns.Zone{Name: z.Name, ProviderID: z.ID, Nameservers: slices.Clone(z.NameServers)})
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

		var raw *http.Response

		res, err := list(
			option.WithQuery("page", strconv.Itoa(page)),
			option.WithQuery("per_page", strconv.Itoa(perPage)),
			option.WithResponseInto(&raw),
		)
		if err != nil {
			return nil, apiError(err, raw)
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

// Records implements provider.Provider. The v4 API reports no SOA serial
// short of a BIND export of the whole Zone, so the RecordSet has none:
// dnscli asks the Zone's nameservers for it.
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

	return provider.RecordSet{Records: records}, nil
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
	return decodeRecord([]byte(r.JSON.RawJSON()))
}

// decodeRecord decodes a v4 DNS record's JSON.
func decodeRecord(data []byte) (dns.Record, error) {
	var w wireRecord
	if err := json.Unmarshal(data, &w); err != nil {
		return dns.Record{}, fmt.Errorf("cloudflare: decode record: %w", err)
	}

	rec := dns.Record{
		ProviderID: w.ID,
		Type:       w.Type,
		Name:       w.Name,
		Value:      w.Content,
		TTL:        w.TTL,
	}

	// Cloudflare keeps the MX, SRV and URI priority out of the content;
	// the Value is in zone-file presentation form, priority first.
	if w.Type == "MX" || w.Type == "SRV" || w.Type == "URI" {
		rec.Value = strconv.FormatFloat(w.Priority, 'f', -1, 64) + " " + w.Content
	}

	if w.Proxiable {
		rec.Extras = map[string]string{proxiedExtra: strconv.FormatBool(w.Proxied)}
	}

	return rec, nil
}

// Apply implements provider.Provider. Each change is its own API call,
// made one at a time: deletions first, then modifications, then additions,
// so a Record that takes the place of another does not collide with it. A
// failed change does not stop the others. The results are in Changeset
// order, and carry no serial: Cloudflare's API does not report it.
func (p *Provider) Apply(ctx context.Context, cs dns.Changeset) (provider.ApplyResult, error) {
	res := provider.ApplyResult{Results: make([]provider.ChangeResult, len(cs.Changes))}

	order := make([]int, len(cs.Changes))
	for i := range order {
		order[i] = i
	}

	slices.SortStableFunc(order, func(a, b int) int {
		return applyRank(cs.Changes[a].Kind) - applyRank(cs.Changes[b].Kind)
	})

	for _, i := range order {
		c := cs.Changes[i]
		rec, err := p.applyChange(ctx, cs.Zone, c)
		res.Results[i] = provider.ChangeResult{Change: c, Record: rec, Err: err}
	}

	return res, nil
}

// applyRank is when Apply makes a change of a kind: lowest first.
func applyRank(k dns.ChangeKind) int {
	switch k {
	case dns.ChangeDelete:
		return 0
	case dns.ChangeModify:
		return 1
	default:
		return 2
	}
}

// applyChange makes one change and returns the Record as Cloudflare
// stored it; an empty Record for a deletion.
//
// A modification is a PATCH of the Record it was built from, so the
// fields dnscli does not know, such as a comment or tags, are kept.
func (p *Provider) applyChange(ctx context.Context, zone dns.Zone, c dns.Change) (dns.Record, error) {
	var (
		env  envelope
		raw  *http.Response
		opts = []option.RequestOption{option.WithResponseBodyInto(&env), option.WithResponseInto(&raw)}
		err  error
	)

	if c.Kind != dns.ChangeAdd && c.Before.ProviderID == "" {
		return dns.Record{}, fmt.Errorf("cloudflare: %s %s has no record ID", c.Before.Type, c.Before.Name)
	}

	if c.Kind != dns.ChangeDelete {
		wb, err := writeBody(c.After)
		if err != nil {
			return dns.Record{}, err
		}

		body, err := json.Marshal(wb)
		if err != nil {
			return dns.Record{}, fmt.Errorf("cloudflare: encode record: %w", err)
		}

		opts = append(opts, option.WithRequestBody("application/json", body))
	}

	switch c.Kind {
	case dns.ChangeAdd:
		var params cfdns.RecordNewParams
		params.ZoneID.Value, params.ZoneID.Present = zone.ProviderID, true
		_, err = p.records.New(ctx, params, opts...)
	case dns.ChangeModify:
		var params cfdns.RecordEditParams
		params.ZoneID.Value, params.ZoneID.Present = zone.ProviderID, true
		_, err = p.records.Edit(ctx, c.Before.ProviderID, params, opts...)
	case dns.ChangeDelete:
		var params cfdns.RecordDeleteParams
		params.ZoneID.Value, params.ZoneID.Present = zone.ProviderID, true
		_, err = p.records.Delete(ctx, c.Before.ProviderID, params, opts...)
	default:
		return dns.Record{}, fmt.Errorf("cloudflare: unknown change kind %d", c.Kind)
	}

	if err != nil {
		return dns.Record{}, apiError(err, raw)
	}

	// Any 2xx is an answer; Cloudflare says in the body whether it took
	// the change.
	if !env.Success {
		e := &provider.APIError{Provider: "cloudflare", StatusCode: raw.StatusCode}
		addErrors(e, env.Errors)

		return dns.Record{}, e
	}

	if c.Kind == dns.ChangeDelete {
		return dns.Record{}, nil
	}

	return decodeRecord(env.Result)
}

// envelope is the v4 response envelope of a record write.
type envelope struct {
	Success bool            `json:"success"`
	Errors  []wireError     `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

// recordBody is the body of a record create or update.
type recordBody struct {
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	Content  string  `json:"content,omitempty"`
	Priority *uint64 `json:"priority,omitempty"`
	Data     any     `json:"data,omitempty"`
	TTL      uint32  `json:"ttl"`
	Proxied  *bool   `json:"proxied,omitempty"`
}

type srvData struct {
	Priority uint64 `json:"priority"`
	Weight   uint64 `json:"weight"`
	Port     uint64 `json:"port"`
	Target   string `json:"target"`
}

type uriData struct {
	Weight uint64 `json:"weight"`
	Target string `json:"target"`
}

type caaData struct {
	Flags uint64 `json:"flags"`
	Tag   string `json:"tag"`
	Value string `json:"value"`
}

// writeBody is the body that creates r, or updates a record to r. It
// undoes what decodeRecord does: the MX, SRV and URI priority leaves the
// Value for its own field, and SRV, URI and CAA Records are written as
// structured data, from which Cloudflare formats their content.
func writeBody(r dns.Record) (recordBody, error) {
	b := recordBody{Type: r.Type, Name: r.Name, TTL: r.TTL}

	if v, ok := r.Extras[proxiedExtra]; ok {
		proxied := v == "true"
		b.Proxied = &proxied
	}

	f := strings.Fields(r.Value)
	bad := fmt.Errorf("cloudflare: %s value %q is not in presentation form", r.Type, r.Value)

	var nums []uint64

	// num parses the first n fields as numbers.
	num := func(n int) bool {
		if len(f) < n {
			return false
		}

		for _, s := range f[:n] {
			v, err := strconv.ParseUint(s, 10, 16)
			if err != nil {
				return false
			}

			nums = append(nums, v)
		}

		return true
	}

	switch r.Type {
	case "MX":
		if len(f) != 2 || !num(1) {
			return b, bad
		}

		b.Priority, b.Content = &nums[0], f[1]
	case "SRV":
		if len(f) != 4 || !num(3) {
			return b, bad
		}

		b.Priority = &nums[0]
		b.Data = srvData{Priority: nums[0], Weight: nums[1], Port: nums[2], Target: f[3]}
	case "URI":
		if len(f) != 3 || !num(2) {
			return b, bad
		}

		b.Priority = &nums[0]
		b.Data = uriData{Weight: nums[1], Target: unquote(f[2])}
	case "CAA":
		if len(f) < 3 || !num(1) {
			return b, bad
		}

		// The value is the rest of the Value, which may hold spaces.
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(r.Value), f[0]))
		rest = strings.TrimSpace(strings.TrimPrefix(rest, f[1]))
		b.Data = caaData{Flags: nums[0], Tag: f[1], Value: unquote(rest)}
	default:
		b.Content = r.Value
	}

	return b, nil
}

// unquote strips the double quotes around a presentation-form string.
func unquote(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return s[1 : len(s)-1]
	}

	return s
}

// ZoneStatus implements provider.Provider. DNSSEC comes from the Zone's
// DNSSEC status and HSTS from its security_header setting, read
// concurrently. A setting the API token lacks the permission to read
// (HTTP 403), or that fails to read while the other one is read, is
// unknown. It fails only when both reads fail.
func (p *Provider) ZoneStatus(ctx context.Context, zone dns.Zone) (dns.ZoneStatus, error) {
	var (
		status  dns.ZoneStatus
		hstsErr error
		wg      sync.WaitGroup
	)

	wg.Add(1)

	go func() {
		defer wg.Done()

		status.HSTS, hstsErr = p.hstsStatus(ctx, zone)
	}()

	dnssec, dnssecErr := p.dnssecStatus(ctx, zone)

	wg.Wait()

	if dnssecErr != nil && hstsErr != nil {
		return dns.ZoneStatus{}, errors.Join(dnssecErr, hstsErr)
	}

	status.DNSSEC = dnssec
	if dnssecErr != nil {
		status.DNSSEC = dns.Unknown
	}

	if hstsErr != nil {
		status.HSTS = dns.Unknown
	}

	return status, nil
}

func (p *Provider) dnssecStatus(ctx context.Context, zone dns.Zone) (dns.Setting, error) {
	var params cfdns.DNSSECGetParams
	params.ZoneID.Value, params.ZoneID.Present = zone.ProviderID, true

	var raw *http.Response

	res, err := p.dnssec.Get(ctx, params, option.WithResponseInto(&raw))
	if err != nil {
		return settingError(err, raw)
	}

	// Pending and pending-disabled are being signed or unsigned; error is
	// neither signed nor unsigned.
	switch res.Status {
	case cfdns.DNSSECStatusActive:
		return dns.On, nil
	case cfdns.DNSSECStatusDisabled:
		return dns.Off, nil
	case cfdns.DNSSECStatusPending, cfdns.DNSSECStatusPendingDisabled:
		return dns.Pending, nil
	}

	return dns.Unknown, nil
}

// securityHeader is the part of the security_header setting dnscli reads.
// It is decoded from the raw JSON: cloudflare-go decodes a setting's value
// into a union of every setting's value.
type securityHeader struct {
	Value struct {
		StrictTransportSecurity struct {
			Enabled bool `json:"enabled"`
		} `json:"strict_transport_security"`
	} `json:"value"`
}

func (p *Provider) hstsStatus(ctx context.Context, zone dns.Zone) (dns.Setting, error) {
	var params zones.SettingGetParams
	params.ZoneID.Value, params.ZoneID.Present = zone.ProviderID, true

	var raw *http.Response

	res, err := p.settings.Get(ctx, "security_header", params, option.WithResponseInto(&raw))
	if err != nil {
		return settingError(err, raw)
	}

	var h securityHeader
	if err := json.Unmarshal([]byte(res.JSON.RawJSON()), &h); err != nil {
		return dns.Unknown, fmt.Errorf("cloudflare: decode security_header setting: %w", err)
	}

	if h.Value.StrictTransportSecurity.Enabled {
		return dns.On, nil
	}

	return dns.Off, nil
}

// settingError makes a setting the API token may not read unknown, and
// any other failure an error.
func settingError(err error, res *http.Response) (dns.Setting, error) {
	if res != nil && res.StatusCode == http.StatusForbidden {
		return dns.Unknown, nil
	}

	return dns.Unknown, apiError(err, res)
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
