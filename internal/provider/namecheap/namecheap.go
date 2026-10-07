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
	"slices"
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

// maxResponseBytes bounds an answer dnscli reads; a larger one is an
// error. A page of 100 domains or a domain's whole host list is far less.
const maxResponseBytes = 4 << 20

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
		PageSize   int `xml:"PageSize"`
	} `xml:"Paging"`
}

// Zones implements provider.Provider: every domain of the Account on
// Namecheap's own DNS. A domain delegated elsewhere has no host list
// Namecheap will read.
func (p *Provider) Zones(ctx context.Context) ([]dns.Zone, error) {
	var (
		out  []dns.Zone
		read int
		last []wireDomain
	)

	for page := 1; ; page++ {
		res, err := call[getListResult](ctx, p, "namecheap.domains.getList", url.Values{
			"Page":     {strconv.Itoa(page)},
			"PageSize": {strconv.Itoa(domainsPerPage)},
		})
		if err != nil {
			return nil, err
		}

		if len(res.Domains) == 0 {
			return out, nil
		}

		if slices.Equal(res.Domains, last) {
			return nil, errors.New("namecheap: the domain list answered the same page twice")
		}

		for _, d := range res.Domains {
			if d.IsOurDNS {
				out = append(out, dns.Zone{Name: d.Name, ProviderID: d.Name})
			}
		}

		// A page shorter than the size Namecheap says it served (or, if it
		// does not say, than asked for) is the last; so is the page that
		// reaches the total, when Namecheap reports one.
		read += len(res.Domains)

		size := res.Paging.PageSize
		if size <= 0 {
			size = domainsPerPage
		}

		if len(res.Domains) < size || (res.Paging.TotalItems > 0 && read >= res.Paging.TotalItems) {
			return out, nil
		}

		last = res.Domains
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
	Result struct {
		// EmailType is the domain's mail setting, such as MX or FWD.
		EmailType string     `xml:"EmailType,attr"`
		Hosts     []wireHost `xml:"host"`
	} `xml:"DomainDNSGetHostsResult"`
}

// Records implements provider.Provider. getHosts answers the whole host
// list at once. Namecheap reports no SOA serial.
func (p *Provider) Records(ctx context.Context, zone dns.Zone) (provider.RecordSet, error) {
	res, err := p.getHosts(ctx, zone)
	if err != nil {
		return provider.RecordSet{}, err
	}

	records := make([]dns.Record, 0, len(res.Result.Hosts))
	for _, h := range res.Result.Hosts {
		records = append(records, toRecord(zone.Name, h))
	}

	return provider.RecordSet{Records: records}, nil
}

// getHosts reads a Zone's whole host list and its EmailType.
func (p *Provider) getHosts(ctx context.Context, zone dns.Zone) (getHostsResult, error) {
	sld, tld := splitDomain(zone.ProviderID)

	return call[getHostsResult](ctx, p, "namecheap.domains.dns.getHosts", url.Values{"SLD": {sld}, "TLD": {tld}})
}

// toRecord turns a host into a Record of a Zone, with an absolute name, its
// HostId as the Provider ID, and its Value in presentation form: the MX and
// SRV priority first, and host names without the trailing dot. A Namecheap-only
// type, such as URL or MXE, keeps its type name and address.
func toRecord(zone string, h wireHost) dns.Record {
	rec := dns.Record{ProviderID: h.HostID, Type: h.Type, Name: provider.AbsoluteName(zone, h.Name), Value: h.Address, TTL: h.TTL}
	pref := strconv.FormatUint(uint64(h.MXPref), 10)

	switch h.Type {
	case "CNAME", "NS", "ALIAS":
		rec.Value = target(h.Address)
	case "MX":
		rec.Value = pref + " " + target(h.Address)
	case "SRV":
		// Namecheap keeps the SRV priority in MXPref, as for MX, and the
		// rest in the address as "weight port target". Any other shape is
		// kept as given.
		if f := strings.Fields(h.Address); len(f) == 3 {
			rec.Value = pref + " " + f[0] + " " + f[1] + " " + target(f[2])
		}
	}

	return rec
}

// target is a host name target without its trailing dot. The root, ".",
// stays "." (a null MX reads "0 .").
func target(name string) string {
	if name == "." {
		return name
	}

	return strings.TrimSuffix(name, ".")
}

// splitDomain splits a domain into Namecheap's SLD, its first label, and
// TLD, the rest. Namecheap registers domains only at public suffixes, so
// "example.co.uk" is "example" and "co.uk".
func splitDomain(domain string) (sld, tld string) {
	sld, tld, _ = strings.Cut(strings.TrimSuffix(domain, "."), ".")

	return sld, tld
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

	// A non-2xx answer is an error page, not XML: its body is not read.
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return zero, &provider.APIError{Provider: "namecheap", StatusCode: res.StatusCode}
	}

	// One byte past the limit tells a cut-off answer from one that fits.
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return zero, fmt.Errorf("namecheap: %w", err)
	}

	if len(data) > maxResponseBytes {
		return zero, fmt.Errorf("namecheap: the %s answer is over %d bytes", command, maxResponseBytes)
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
