package traffic

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
)

// PSP's own node and relay addresses, kept so the location detector can set
// them aside.
//
// Traffic that reaches a landing through one of PSP's relays arrives from the
// RELAY's address, not the user's. Judged as a place, every user behind a
// relay in another country would read as "in two countries at once" — the
// most common false positive there is, and one PSP can remove on its own
// because it already knows every node and relay it renders. The shared-exit
// rule and the admin ignore list catch what this cannot see (a CDN, an
// operator's own forwarder the panel does not know about).
const (
	// infraHostTTL is how long a hostname's answer is reused. Relay
	// hostnames rarely move, and the refresh loop runs far more often than
	// this, so most refreshes do no DNS at all.
	infraHostTTL = 10 * time.Minute
	// infraResolveTimeout bounds one lookup. DNS only: the refresh never
	// dials anything.
	infraResolveTimeout = 2 * time.Second
	// infraResolveConcurrency bounds the lookups one refresh runs at once,
	// so a deployment with many relay hostnames does not fire them all at
	// the resolver together.
	infraResolveConcurrency = 4
)

// hostResolution is one hostname's last good answer and when to ask again.
type hostResolution struct {
	addrs     []netip.Addr
	expiresAt time.Time
}

// infraAddressSet is the current set of infrastructure addresses.
//
// Written only by the refresh loop and read by every poll, so reads are a
// map lookup under a read lock and never do I/O: resolving hostnames on the
// poll path would put DNS latency (and DNS failure) inside the traffic
// poll. The refresh builds the new set aside and swaps it in whole, so a
// reader sees either the old set or the new one, never half of each.
type infraAddressSet struct {
	mu      sync.RWMutex
	current map[netip.Addr]struct{}
	hosts   map[string]hostResolution
	// resolve and now are replaceable for tests; nil means the real
	// resolver and clock, so a zero value still works.
	resolve func(ctx context.Context, host string) ([]string, error)
	now     func() time.Time
}

func newInfraAddressSet() *infraAddressSet {
	return &infraAddressSet{
		resolve: net.DefaultResolver.LookupHost,
		now:     time.Now,
	}
}

// Contains reports whether a is one of PSP's node or relay addresses.
// Nil-safe: before the first refresh (or with no set wired) nothing is
// infrastructure. The address is unmapped and its zone dropped, so the
// forms a panel reports ("::ffff:1.2.3.4", "fe80::1%eth0") match the forms
// an admin typed.
func (c *infraAddressSet) Contains(a netip.Addr) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.current[a.Unmap().WithZone("")]
	return ok
}

// RefreshInfraAddresses rebuilds the infrastructure set from the node list.
// Called by the app's refresh loop, never by the poll. A node list that
// cannot be read leaves the previous set in place and returns the error, so
// one database hiccup does not un-exclude every relay for a cycle.
func (s *Service) RefreshInfraAddresses(ctx context.Context) error {
	if s.nodes == nil || s.infra == nil {
		return nil
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return fmt.Errorf("list nodes for infrastructure addresses: %w", err)
	}
	s.infra.refresh(ctx, nodes)
	return nil
}

// refresh collects every enabled node's own address and every enabled relay
// in front of it, resolves the hostnames among them, and swaps the result in.
//
// What counts mirrors what can carry traffic: a separator is a label, not a
// server; a disabled node or relay renders nothing. The node's direct address
// counts even when the subscription hides it (HideDirect), because the
// health probe still dials it and so, often, do clients with an old
// subscription. Disabled ones are deliberately NOT kept: an address PSP does
// not route through is not an exit, and excluding it would hide a user who
// really is there.
func (c *infraAddressSet) refresh(ctx context.Context, nodes []*domain.Node) {
	resolve := c.resolve
	if resolve == nil {
		resolve = net.DefaultResolver.LookupHost
	}
	clock := c.now
	if clock == nil {
		clock = time.Now
	}

	literals := map[netip.Addr]struct{}{}
	names := map[string]struct{}{}
	collect := func(raw string) {
		h := normaliseInfraHost(raw)
		if h == "" {
			return
		}
		if a, err := netip.ParseAddr(h); err == nil {
			literals[a.Unmap().WithZone("")] = struct{}{}
			return
		}
		names[h] = struct{}{}
	}
	for _, n := range nodes {
		if n == nil || n.IsSeparator() || !n.Enabled {
			continue
		}
		collect(n.ServerAddress)
		for _, r := range n.Relays {
			if r.Enabled {
				collect(r.Address)
			}
		}
	}

	now := clock()
	c.mu.RLock()
	prev := c.hosts
	c.mu.RUnlock()

	// Only hostnames still referenced are carried over, so a relay the admin
	// removed leaves the cache (and the set) on this refresh.
	hosts := make(map[string]hostResolution, len(names))
	var due []string
	for h := range names {
		if r, ok := prev[h]; ok && now.Before(r.expiresAt) {
			hosts[h] = r
			continue
		}
		due = append(due, h)
	}

	results := make([]hostResolution, len(due))
	failed := make([]error, len(due))
	sem := make(chan struct{}, infraResolveConcurrency)
	var wg sync.WaitGroup
	for i, h := range due {
		wg.Add(1)
		go func() {
			defer safego.Recover("traffic.infraResolve")
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rctx, cancel := context.WithTimeout(ctx, infraResolveTimeout)
			defer cancel()
			raw, err := resolve(rctx, h)
			if err != nil {
				failed[i] = err
				return
			}
			addrs := make([]netip.Addr, 0, len(raw))
			for _, s := range raw {
				if a, perr := netip.ParseAddr(strings.TrimSpace(s)); perr == nil {
					addrs = append(addrs, a.Unmap().WithZone(""))
				}
			}
			results[i] = hostResolution{addrs: addrs, expiresAt: now.Add(infraHostTTL)}
		}()
	}
	wg.Wait()

	for i, h := range due {
		if err := failed[i]; err != nil {
			// Keep the last good answer, expiry included: the entry stays
			// due, so the next refresh retries rather than waiting out a
			// fresh TTL on stale data. Dropping it would turn one DNS
			// hiccup into every user behind that relay suddenly judged
			// from the relay's country.
			if r, ok := prev[h]; ok {
				hosts[h] = r
			}
			metrics.InfraAddressResolveFailuresTotal.Inc()
			log.Warn("infra addresses: could not resolve a node or relay hostname; keeping its previous addresses",
				"host", h, "err", err)
			continue
		}
		hosts[h] = results[i]
	}

	current := make(map[netip.Addr]struct{}, len(literals)+len(hosts))
	for a := range literals {
		current[a] = struct{}{}
	}
	for _, r := range hosts {
		for _, a := range r.addrs {
			current[a] = struct{}{}
		}
	}

	c.mu.Lock()
	c.current = current
	c.hosts = hosts
	c.mu.Unlock()
	metrics.InfraAddresses.Set(int64(len(current)))
}

// normaliseInfraHost turns an address as an admin typed it into a lookup
// key: trimmed, brackets stripped ("[2001:db8::1]"), one trailing dot
// dropped ("relay.example.com."), lower-cased so one host typed two ways is
// looked up once.
func normaliseInfraHost(raw string) string {
	h := strings.TrimSpace(raw)
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	h = strings.TrimSuffix(h, ".")
	return strings.ToLower(h)
}
