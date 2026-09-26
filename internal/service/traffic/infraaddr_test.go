package traffic

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// infraNodes is a NodeRepo that only lists. Everything else panics through
// the nil embedded interface, which is the point: the refresh must need
// nothing but List.
type infraNodes struct {
	ports.NodeRepo
	mu    sync.Mutex
	nodes []*domain.Node
	err   error
}

func (r *infraNodes) List(context.Context) ([]*domain.Node, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	out := make([]*domain.Node, len(r.nodes))
	copy(out, r.nodes)
	return out, nil
}

func (r *infraNodes) set(nodes ...*domain.Node) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes = nodes
}

func (r *infraNodes) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

// fakeResolver answers from a table and counts every lookup per host. Safe
// for the refresh's concurrent lookups.
type fakeResolver struct {
	mu      sync.Mutex
	answers map[string][]string
	err     error
	calls   map[string]int
}

func (r *fakeResolver) resolve(_ context.Context, host string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[host]++
	if r.err != nil {
		return nil, r.err
	}
	a, ok := r.answers[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return a, nil
}

func (r *fakeResolver) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		n += c
	}
	return n
}

func (r *fakeResolver) failWith(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func infraNode(id int64, server string, relays ...domain.RelayLine) *domain.Node {
	return &domain.Node{ID: id, Enabled: true, ServerAddress: server, Relays: relays}
}

func relayAt(addr string, enabled bool) domain.RelayLine {
	return domain.RelayLine{Address: addr, Enabled: enabled}
}

func newInfraFixture(nodes ...*domain.Node) (*Service, *infraNodes, *fakeResolver, *fakeClock) {
	repo := &infraNodes{nodes: nodes}
	res := &fakeResolver{answers: map[string][]string{}, calls: map[string]int{}}
	clk := &fakeClock{t: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)}
	set := newInfraAddressSet()
	set.resolve = res.resolve
	set.now = clk.now
	return &Service{nodes: repo, infra: set}, repo, res, clk
}

func refreshOK(t *testing.T, s *Service) {
	t.Helper()
	if err := s.RefreshInfraAddresses(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

func gaugeFor(t *testing.T, name string) int64 {
	t.Helper()
	for _, g := range metrics.Take().Gauges {
		if g.Name == name {
			return g.Value
		}
	}
	t.Fatalf("gauge %s is not registered", name)
	return 0
}

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

// Every enabled node's own address, and every ENABLED relay in front of it.
// The direct address counts even when the subscription hides it: the
// health probe dials it regardless, so it is live infrastructure.
func TestInfraAddresses_CollectsEnabledNodesAndEnabledRelays(t *testing.T) {
	metrics.InfraAddresses.Set(0)
	n := infraNode(1, "203.0.113.1", relayAt("198.51.100.7", true), relayAt("198.51.100.8", false))
	n.HideDirect = true
	s, _, _, _ := newInfraFixture(n)
	refreshOK(t, s)

	for ip, want := range map[string]bool{
		"203.0.113.1":  true,
		"198.51.100.7": true,
		"198.51.100.8": false, // a disabled relay carries no traffic
	} {
		if got := s.infra.Contains(mustAddr(ip)); got != want {
			t.Errorf("Contains(%s) = %v, want %v", ip, got, want)
		}
	}
	if got := gaugeFor(t, "psp_infra_addresses"); got != 2 {
		t.Fatalf("psp_infra_addresses = %d, want 2", got)
	}
}

// A separator is a label in the node list, not a server; a disabled node
// carries no traffic, and neither do its relays.
func TestInfraAddresses_SkipsSeparatorsAndDisabled(t *testing.T) {
	sep := infraNode(2, "203.0.113.2")
	sep.Kind = domain.NodeKindSeparator
	off := infraNode(3, "203.0.113.3", relayAt("198.51.100.9", true))
	off.Enabled = false
	s, _, _, _ := newInfraFixture(nil, sep, off, infraNode(4, "203.0.113.4"))
	refreshOK(t, s)

	for ip, want := range map[string]bool{
		"203.0.113.2":  false,
		"203.0.113.3":  false,
		"198.51.100.9": false,
		"203.0.113.4":  true,
	} {
		if got := s.infra.Contains(mustAddr(ip)); got != want {
			t.Errorf("Contains(%s) = %v, want %v", ip, got, want)
		}
	}
}

// DNS belongs to the refresh loop, never to the poll: Contains runs once per
// source per user per poll and must be a map lookup.
func TestInfraAddresses_ResolvesHostnamesOnRefreshNotOnRead(t *testing.T) {
	s, _, res, _ := newInfraFixture(infraNode(1, "edge.example.com"))
	res.answers["edge.example.com"] = []string{"203.0.113.5"}

	if s.infra.Contains(mustAddr("203.0.113.5")) || res.total() != 0 {
		t.Fatalf("before any refresh: contained or resolved (%d lookups)", res.total())
	}
	refreshOK(t, s)
	if res.total() != 1 {
		t.Fatalf("lookups after one refresh = %d, want 1", res.total())
	}
	for i := 0; i < 100; i++ {
		if !s.infra.Contains(mustAddr("203.0.113.5")) {
			t.Fatal("the resolved address is not in the set")
		}
	}
	if res.total() != 1 {
		t.Fatalf("lookups after 100 reads = %d, want still 1", res.total())
	}
}

// A lookup that fails keeps what the host resolved to last time. Dropping
// it would turn one DNS hiccup into every user behind that relay suddenly
// "in two places". The failure is counted, and the next refresh retries.
func TestInfraAddresses_ResolveFailureKeepsThePreviousAddresses(t *testing.T) {
	metrics.Reset()
	s, _, res, clk := newInfraFixture(infraNode(1, "edge.example.com"))
	res.answers["edge.example.com"] = []string{"203.0.113.5"}
	refreshOK(t, s)

	clk.advance(infraHostTTL + time.Second)
	res.failWith(errors.New("SERVFAIL"))
	refreshOK(t, s)
	if !s.infra.Contains(mustAddr("203.0.113.5")) {
		t.Fatal("a failed lookup dropped the previous addresses")
	}
	if got := counterFor(t, "psp_infra_address_resolve_failures_total"); got != 1 {
		t.Fatalf("resolve failures = %d, want 1", got)
	}

	refreshOK(t, s) // no time passes: the kept entry is still expired, so it is retried
	if res.total() != 3 {
		t.Fatalf("lookups = %d, want 3 (the failed host is retried on the next refresh)", res.total())
	}
}

// A relay the admin removed is no longer infrastructure, and its hostname
// leaves the cache with it.
func TestInfraAddresses_RemovedRelayLeavesTheSet(t *testing.T) {
	s, repo, res, _ := newInfraFixture(infraNode(1, "203.0.113.1", relayAt("relay.example.com", true)))
	res.answers["relay.example.com"] = []string{"198.51.100.7"}
	refreshOK(t, s)
	if !s.infra.Contains(mustAddr("198.51.100.7")) {
		t.Fatal("the relay's address was not collected")
	}

	repo.set(infraNode(1, "203.0.113.1"))
	refreshOK(t, s)
	if s.infra.Contains(mustAddr("198.51.100.7")) {
		t.Fatal("a removed relay's address is still excluded")
	}
	if !s.infra.Contains(mustAddr("203.0.113.1")) {
		t.Fatal("the node's own address was lost with the relay")
	}
	s.infra.mu.RLock()
	cached := len(s.infra.hosts)
	s.infra.mu.RUnlock()
	if cached != 0 {
		t.Fatalf("hostname cache still holds %d entries after the relay was removed", cached)
	}
}

// Within its TTL a hostname is reused, not looked up again every tick.
func TestInfraAddresses_TTLAvoidsReResolving(t *testing.T) {
	s, _, res, clk := newInfraFixture(infraNode(1, "edge.example.com"))
	res.answers["edge.example.com"] = []string{"203.0.113.5"}
	refreshOK(t, s)
	clk.advance(infraHostTTL - time.Second)
	refreshOK(t, s)
	if res.total() != 1 {
		t.Fatalf("lookups inside the TTL = %d, want 1", res.total())
	}
	clk.advance(2 * time.Second)
	refreshOK(t, s)
	if res.total() != 2 {
		t.Fatalf("lookups after the TTL = %d, want 2", res.total())
	}
}

// Addresses as admins actually type them: a bracketed IPv6 literal, a
// link-local one with a zone, an IPv4-mapped one, and a hostname with odd
// case, spaces and a trailing dot. And addresses as the poll asks about them:
// mapped or zoned forms of the same address.
func TestInfraAddresses_IPv6LiteralsBracketsAndZones(t *testing.T) {
	s, _, res, _ := newInfraFixture(infraNode(1, "[2001:db8::1]",
		relayAt("fe80::1%eth0", true),
		relayAt("::ffff:192.0.2.1", true),
		relayAt("  HOST.Example.COM. ", true),
	))
	res.answers["host.example.com"] = []string{"2001:db8::2", "::ffff:192.0.2.9"}
	refreshOK(t, s)

	for _, ip := range []string{
		"2001:db8::1", "fe80::1", "192.0.2.1", "2001:db8::2", "192.0.2.9",
		"::ffff:192.0.2.1", "fe80::1%eth1",
	} {
		if !s.infra.Contains(mustAddr(ip)) {
			t.Errorf("Contains(%s) = false, want true", ip)
		}
	}
	res.mu.Lock()
	calls := res.calls["host.example.com"]
	res.mu.Unlock()
	if calls != 1 {
		t.Fatalf("the hostname was not normalised before lookup: calls = %v", res.calls)
	}
}

// A node list that cannot be read changes nothing: the previous set stays,
// and the error is reported to the loop.
func TestInfraAddresses_NodeListFailureKeepsTheSet(t *testing.T) {
	metrics.InfraAddresses.Set(0)
	s, repo, _, _ := newInfraFixture(infraNode(1, "203.0.113.1"))
	refreshOK(t, s)

	boom := errors.New("db down")
	repo.fail(boom)
	if err := s.RefreshInfraAddresses(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("refresh err = %v, want it to wrap %v", err, boom)
	}
	if !s.infra.Contains(mustAddr("203.0.113.1")) {
		t.Fatal("a failed node list emptied the set")
	}
	if got := gaugeFor(t, "psp_infra_addresses"); got != 1 {
		t.Fatalf("psp_infra_addresses = %d, want 1", got)
	}
}

// A set built without newInfraAddressSet still refreshes, with the real
// clock and resolver as defaults. Literal addresses only, so no DNS runs.
func TestInfraAddresses_ZeroValueSetRefreshes(t *testing.T) {
	s := &Service{nodes: &infraNodes{nodes: []*domain.Node{infraNode(1, "203.0.113.1")}}, infra: &infraAddressSet{}}
	refreshOK(t, s)
	if !s.infra.Contains(mustAddr("203.0.113.1")) {
		t.Fatal("a zero-value set did not take the refresh")
	}
}

// Guard: the poll asks before the first refresh has run, and a bare
// &Service{} has no set at all.
func TestInfraAddresses_ContainsIsNilSafe(t *testing.T) {
	var none *infraAddressSet
	if none.Contains(mustAddr("203.0.113.1")) {
		t.Fatal("a nil set contains an address")
	}
	if (&infraAddressSet{}).Contains(mustAddr("203.0.113.1")) {
		t.Fatal("an empty set contains an address")
	}
	if err := (&Service{}).RefreshInfraAddresses(context.Background()); err != nil {
		t.Fatalf("refresh with nothing wired: %v", err)
	}
	if err := (&Service{nodes: &infraNodes{}}).RefreshInfraAddresses(context.Background()); err != nil {
		t.Fatalf("refresh with no set: %v", err)
	}
}

// Guard: the refresh loop swaps the set while every poll reads it. Run with
// -race; dropping the read lock in Contains fails it.
func TestInfraAddresses_RefreshAndReadDoNotRace(t *testing.T) {
	s, _, res, clk := newInfraFixture(infraNode(1, "edge.example.com", relayAt("198.51.100.7", true)))
	res.answers["edge.example.com"] = []string{"203.0.113.5"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			clk.advance(infraHostTTL)
			_ = s.RefreshInfraAddresses(context.Background())
		}
	}()
	for {
		select {
		case <-done:
			return
		default:
			s.infra.Contains(mustAddr("203.0.113.5"))
		}
	}
}
