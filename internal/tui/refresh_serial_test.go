package tui_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dnsgateway/dnsprovider"

	"github.com/sergelogvinov/dnscli/internal/tui"
)

// serialFirst is a resolver that says when the Zone's nameservers are
// asked for its serial, and serialFirstRecords a Provider whose Records
// answer only once they are, so a Refresh that asks one after the other
// fails.
type serialFirst struct {
	*fakeResolver

	once  sync.Once
	asked chan struct{}
}

func (r *serialFirst) SOAAt(ctx context.Context, zone string, servers []string) (uint32, error) {
	r.once.Do(func() { close(r.asked) })

	return r.fakeResolver.SOAAt(ctx, zone, servers)
}

type serialFirstRecords struct {
	dnsprovider.Provider

	asked chan struct{}
}

func (p *serialFirstRecords) Records(ctx context.Context, z dnsprovider.Zone, rf dnsprovider.RecordFilter) (dnsprovider.RecordSet, error) {
	select {
	case <-p.asked:
		return p.Provider.Records(ctx, z, rf)
	case <-time.After(5 * time.Second):
		return dnsprovider.RecordSet{}, errors.New("the serial was not asked for alongside the Records")
	}
}

func TestARefreshAsksForTheSerialAlongsideTheRecords(t *testing.T) {
	f := serialZones(zoneNS)
	r := &serialFirst{fakeResolver: &fakeResolver{soaAt: map[string]uint32{"work.example": 2026100799}}, asked: make(chan struct{})}

	s, _ := openStore(t)
	h := newHarnessWith(t, s, tui.Deps{
		Providers: wrapped(f, func(p dnsprovider.Provider) dnsprovider.Provider {
			return &serialFirstRecords{Provider: p, asked: r.asked}
		}),
		UnsealMethod: fastPassphrase(),
		Resolver:     r,
	})
	h.fillAddAccount("work", token)
	h.typeText(passphrase)
	h.press("tab")
	h.typeText(passphrase)
	h.press("enter")

	h.press("enter")
	h.assertShows("validated this session", "203.0.113.20")

	if z := cachedZone(t, s, "work.example"); z.Serial != 2026100799 {
		t.Fatalf("cached serial = %d, want 2026100799 from the nameservers", z.Serial)
	}
}

func TestARefreshDoesNotAskNameserversThatFailedForAFewMinutes(t *testing.T) {
	now := testNow
	r := &fakeResolver{soaAtErrs: map[string]error{"work.example": errors.New("i/o timeout")}}
	h, _, _ := newRefreshHarnessWith(t, serialZones(zoneNS), tui.Deps{Resolver: r, Clock: func() time.Time { return now }})

	h.press("enter")
	h.assertShows("validated this session")

	if n := len(r.soaAtQueries()); n != 1 {
		t.Fatalf("SOA queries to the nameservers = %d, want 1", n)
	}

	// A minute later, a Refresh does not wait on them again.
	now = now.Add(time.Minute)
	h.press("r")
	h.assertShows("validated this session")

	if n := len(r.soaAtQueries()); n != 1 {
		t.Fatalf("SOA queries to the nameservers = %d, want still 1", n)
	}

	// Some minutes later, they are asked again.
	now = now.Add(5 * time.Minute)
	h.press("r")
	h.assertShows("validated this session")

	if n := len(r.soaAtQueries()); n != 2 {
		t.Fatalf("SOA queries to the nameservers = %d, want 2", n)
	}
}
