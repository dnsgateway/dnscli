// Package namecheap is the Namecheap Provider, on the Namecheap XML API
// over plain net/http: Namecheap has no Go SDK of its own.
package namecheap

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sergelogvinov/dnscli/internal/dns"
	"github.com/sergelogvinov/dnscli/internal/provider"
)

// DefaultBaseURL is the Namecheap production API endpoint. The sandbox is
// https://api.sandbox.namecheap.com/xml.response.
const DefaultBaseURL = "https://api.namecheap.com/xml.response"

// DefaultRequestTimeout bounds each request to the Namecheap API.
const DefaultRequestTimeout = 30 * time.Second

// domainsPerPage is the largest page Namecheap serves for domains.
const domainsPerPage = 100

// Config configures the Namecheap Provider. Its zero value talks to the
// real Namecheap API.
type Config struct {
	// BaseURL is the API endpoint, ending in /xml.response; empty means
	// DefaultBaseURL.
	BaseURL string
	// HTTPClient sends the requests; nil means a client that times out
	// each request after DefaultRequestTimeout.
	HTTPClient *http.Client
}

// errTokenShape is the error for a token New does not take.
var errTokenShape = errors.New("namecheap: the API token is the API user, API key and whitelisted IPv4 client IP as APIUSER:APIKEY:CLIENTIP")

// New is the provider.Constructor for Namecheap. The token is the API
// user, the API key and the client IP whitelisted for the API, as
// APIUSER:APIKEY:CLIENTIP. dnscli does not look up its own public IP.
func (c Config) New(_ dns.Account, token string) (provider.Provider, error) {
	parts := strings.Split(token, ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return nil, errTokenShape
	}

	ip, err := netip.ParseAddr(parts[2])
	if err != nil || !ip.Is4() {
		return nil, errTokenShape
	}

	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultRequestTimeout}
	}

	return &Provider{
		baseURL: baseURL,
		auth: url.Values{
			"ApiUser":  {parts[0]},
			"UserName": {parts[0]},
			"ApiKey":   {parts[1]},
			"ClientIp": {parts[2]},
		},
		client: client,
	}, nil
}

// Provider is one Namecheap Account.
type Provider struct {
	baseURL string
	// auth are the parameters every request carries.
	auth   url.Values
	client *http.Client
}

var (
	_ provider.Provider  = (*Provider)(nil)
	_ provider.TTLLimits = (*Provider)(nil)
)

// TTLRange implements provider.TTLLimits. Namecheap's "Automatic" TTL is
// 1799 seconds, an ordinary TTL to dnscli.
func (p *Provider) TTLRange() (minTTL, maxTTL uint32) {
	return 60, 60000
}

// wireDomain is the part of a getList domain that dnscli reads.
type wireDomain struct {
	Name     string `xml:"Name,attr"`
	IsOurDNS bool   `xml:"IsOurDNS,attr"`
}

// getListResult is a getList CommandResponse.
type getListResult struct {
	Domains []wireDomain `xml:"DomainGetListResult>Domain"`
	Paging  struct {
		TotalItems int `xml:"TotalItems"`
	} `xml:"Paging"`
}

// Zones implements provider.Provider: every domain of the Account on
// Namecheap's own DNS. A domain delegated elsewhere has no host list
// Namecheap will read.
func (p *Provider) Zones(ctx context.Context) ([]dns.Zone, error) {
	var out []dns.Zone

	for page := 1; ; page++ {
		res, err := call[getListResult](ctx, p, "namecheap.domains.getList", url.Values{
			"Page":     {strconv.Itoa(page)},
			"PageSize": {strconv.Itoa(domainsPerPage)},
		})
		if err != nil {
			return nil, err
		}

		for _, d := range res.Domains {
			if d.IsOurDNS {
				out = append(out, dns.Zone{Name: d.Name, ProviderID: d.Name})
			}
		}

		// A short page is the last; so is the page that reaches the total.
		if len(res.Domains) < domainsPerPage || page*domainsPerPage >= res.Paging.TotalItems {
			return out, nil
		}
	}
}

// wireHost is one host of a getHosts answer. Its name is relative to the
// domain, "@" for the apex.
type wireHost struct {
	HostID  string `xml:"HostId,attr"`
	Name    string `xml:"Name,attr"`
	Type    string `xml:"Type,attr"`
	Address string `xml:"Address,attr"`
	MXPref  uint32 `xml:"MXPref,attr"`
	TTL     uint32 `xml:"TTL,attr"`
}

// getHostsResult is a getHosts CommandResponse.
type getHostsResult struct {
	Hosts []wireHost `xml:"DomainDNSGetHostsResult>host"`
}

// Records implements provider.Provider. getHosts answers the whole host
// list at once. Namecheap reports no SOA serial.
func (p *Provider) Records(ctx context.Context, zone dns.Zone) (provider.RecordSet, error) {
	sld, tld := splitDomain(zone.ProviderID)

	res, err := call[getHostsResult](ctx, p, "namecheap.domains.dns.getHosts", url.Values{"SLD": {sld}, "TLD": {tld}})
	if err != nil {
		return provider.RecordSet{}, err
	}

	records := make([]dns.Record, 0, len(res.Hosts))
	for _, h := range res.Hosts {
		records = append(records, toRecord(zone.Name, h))
	}

	return provider.RecordSet{Records: records}, nil
}

// toRecord turns a host into a Record of a Zone, with an absolute name, its
// HostId as the Provider ID, and its Value in presentation form: the MX
// priority first, and host names without the trailing dot. A Namecheap-only
// type, such as URL or MXE, keeps its type name and address.
func toRecord(zone string, h wireHost) dns.Record {
	rec := dns.Record{ProviderID: h.HostID, Type: h.Type, Name: absolute(zone, h.Name), Value: h.Address, TTL: h.TTL}

	switch h.Type {
	case "CNAME", "NS", "ALIAS":
		rec.Value = strings.TrimSuffix(h.Address, ".")
	case "MX":
		rec.Value = strconv.FormatUint(uint64(h.MXPref), 10) + " " + strings.TrimSuffix(h.Address, ".")
	}

	return rec
}

// absolute turns a host name relative to a Zone into an absolute one.
func absolute(zone, name string) string {
	if name == "@" || name == "" {
		return zone
	}

	return name + "." + zone
}

// splitDomain splits a domain into Namecheap's SLD, its first label, and
// TLD, the rest. Namecheap registers domains only at public suffixes, so
// "example.co.uk" is "example" and "co.uk".
func splitDomain(domain string) (sld, tld string) {
	sld, tld, _ = strings.Cut(strings.TrimSuffix(domain, "."), ".")

	return sld, tld
}

// Apply implements provider.Provider. Namecheap writes are not supported
// yet: every change fails and nothing is sent.
func (p *Provider) Apply(_ context.Context, cs dns.Changeset) (provider.ApplyResult, error) {
	res := provider.ApplyResult{Results: make([]provider.ChangeResult, len(cs.Changes))}

	for i, c := range cs.Changes {
		res.Results[i] = provider.ChangeResult{
			Change: c,
			Err:    fmt.Errorf("namecheap: writing records is %w", provider.ErrNotImplemented),
		}
	}

	return res, nil
}

// ZoneStatus implements provider.Provider. getHosts reports neither
// DNSSEC nor HSTS, and DNSSEC is a registrar setting (ADR-0004), so both
// are unknown.
func (p *Provider) ZoneStatus(context.Context, dns.Zone) (dns.ZoneStatus, error) {
	return dns.ZoneStatus{DNSSEC: dns.Unknown, HSTS: dns.Unknown}, nil
}

// Nameservers implements provider.Provider: Namecheap's BasicDNS
// nameservers, the same for every Account.
func (p *Provider) Nameservers(context.Context) ([]string, error) {
	return []string{"dns1.registrar-servers.com", "dns2.registrar-servers.com"}, nil
}

// apiResponse is the envelope of every answer, around a command's own
// response. Namecheap answers HTTP 200 whether or not the command worked;
// Status says which.
type apiResponse[T any] struct {
	Status          string      `xml:"Status,attr"`
	Errors          []wireError `xml:"Errors>Error"`
	CommandResponse T           `xml:"CommandResponse"`
}

// wireError is one error of an ERROR answer.
type wireError struct {
	Number string `xml:"Number,attr"`
	Text   string `xml:",chardata"`
}

// call sends a command with its parameters and the Account's, and decodes
// its CommandResponse. The parameters go in a POST form, so the API key
// stays out of URLs and the errors that quote them.
func call[T any](ctx context.Context, p *Provider, command string, params url.Values) (T, error) {
	var zero T

	form := url.Values{"Command": {command}}
	maps.Copy(form, p.auth)
	maps.Copy(form, params)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, strings.NewReader(form.Encode()))
	if err != nil {
		return zero, fmt.Errorf("namecheap: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := p.client.Do(req)
	if err != nil {
		return zero, fmt.Errorf("namecheap: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return zero, fmt.Errorf("namecheap: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return zero, &provider.APIError{Provider: "namecheap", StatusCode: res.StatusCode}
	}

	var body apiResponse[T]
	if err := xml.Unmarshal(data, &body); err != nil {
		return zero, fmt.Errorf("namecheap: decode %s: %w", command, err)
	}

	if !strings.EqualFold(body.Status, "OK") {
		return zero, statusError(body.Errors)
	}

	return body.CommandResponse, nil
}
