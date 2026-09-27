package traffic

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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
//
// The set also remembers which addresses are LANDING nodes (a node's own
// server) as opposed to relays, for the risk worker's login check, which
// skips the landings' countries but not the relays'.
//
// How long a hostname's answer is reused is geo_anomaly.infra_host_ttl_minutes
// (ten minutes by default), read on every refresh: relay hostnames rarely
// move, and the refresh loop runs more often than that, so most refreshes do
// no DNS at all.
const (
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
	// landing is the subset of current that came from a node's own
	// ServerAddress — a literal, or what a hostname named there resolved
	// to — as opposed to a relay in front of it. The login check skips the
	// countries of landings only (see Landing).
	landing map[netip.Addr]struct{}
	hosts   map[string]hostResolution
	// loaded is set by the first swap and never cleared. Until then the set
	// is empty because nothing has been collected yet, not because nothing
	// is infrastructure — a difference the poll can live with (it has always
	// judged an early poll unfiltered) but the risk worker must not: its
	// place signals judge a whole week at once, so one run against an empty
	// set files every relayed user under the relay's province. A refresh
	// whose node list failed swaps nothing and leaves it false; an empty
	// node list is an answer, and sets it.
	loaded bool
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

// Loaded reports whether a refresh has ever swapped a set in (see loaded).
// Nil-safe: no set is never loaded.
func (c *infraAddressSet) Loaded() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loaded
}

// InfraLoaded reports whether the infrastructure set has been built at least
// once, so a reader that must not judge against a not-yet-collected set (the
// risk worker's place signals) can wait for it. False on a service with no
// set, and while the node list has never been read.
func (s *Service) InfraLoaded() bool {
	return s != nil && s.infra.Loaded()
}

// IsInfra reports whether a is one of PSP's node or relay addresses — the
// set the poll excludes, handed to the risk worker as a plain function so it
// never holds the traffic service itself. A map lookup under a read lock; the
// DNS behind it ran in the refresh loop.
func (s *Service) IsInfra(a netip.Addr) bool {
	return s != nil && s.infra.Contains(a)
}

// Landing returns the addresses of PSP's landing nodes — every enabled
// node's own ServerAddress, resolved — as a sorted copy. Relays are left
// out: the risk worker skips a panel login from a landing's COUNTRY (an
// account holder's browser often reaches the panel through their own
// proxy, whose egress is the landing's), and relays usually sit in the
// account holder's own country, so counting them would skip every login
// from home. Nil-safe; nil before the first refresh.
func (c *infraAddressSet) Landing() []netip.Addr {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.landing) == 0 {
		return nil
	}
	out := make([]netip.Addr, 0, len(c.landing))
	for a := range c.landing {
		out = append(out, a)
	}
	slices.SortFunc(out, netip.Addr.Compare)
	return out
}

// LandingAddresses reports the addresses of PSP's own landing nodes (see
// infraAddressSet.Landing), handed to the risk worker as a plain function
// like IsInfra. A copy, taken under the read lock; no I/O.
func (s *Service) LandingAddresses() []netip.Addr {
	if s == nil {
		return nil
	}
	return s.infra.Landing()
}

// RefreshInfraAddresses rebuilds the infrastructure set from the node list.
// Called by the app's refresh loop, never by the poll. A node list that
// cannot be read leaves the previous set in place and returns the error, so
// one database hiccup does not un-exclude every relay for a cycle.
//
// The hostname TTL is the fleet's geo_anomaly.infra_host_ttl_minutes, from a
// global settings read here. No settings wired, or a read that fails, means
// the shipped default: the TTL only decides how often DNS is asked, and an
// unreadable setting is no reason to fail the refresh or to hammer the
// resolver.
func (s *Service) RefreshInfraAddresses(ctx context.Context) error {
	if s.nodes == nil || s.infra == nil {
		return nil
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return fmt.Errorf("list nodes for infrastructure addresses: %w", err)
	}
	var set ports.UISettings
	if s.settings != nil {
		if loaded, lerr := s.settings.Load(ctx, ports.UISettings{}); lerr == nil {
			set = loaded
		} else {
			log.Warn("infra addresses: could not read the settings; using the default hostname TTL", "err", lerr)
		}
	}
	s.infra.refresh(ctx, nodes, domain.GeoRuntimeFromSettings(set.GeoRuntimeSettings()).InfraHostTTL)
	return nil
}

// refresh collects every enabled node's own address and every enabled relay
// in front of it, resolves the hostnames among them, and swaps the result in
// — together with the landing subset, the addresses that came from a node's
// own ServerAddress rather than a relay (see Landing).
//
// What counts mirrors what can carry traffic: a separator is a label, not a
// server; a disabled node or relay renders nothing. The node's direct address
// counts even when the subscription hides it (HideDirect), because the
// health probe still dials it and so, often, do clients with an old
// subscription. Disabled ones are deliberately NOT kept: an address PSP does
// not route through is not an exit, and excluding it would hide a user who
// really is there.
//
// A hostname resolved on this refresh is reused for ttl; one cached by an
// earlier refresh keeps the expiry it was given then, so a changed TTL takes
// effect as each name comes due.
func (c *infraAddressSet) refresh(ctx context.Context, nodes []*domain.Node, ttl time.Duration) {
	resolve := c.resolve
	if resolve == nil {
		resolve = net.DefaultResolver.LookupHost
	}
	clock := c.now
	if clock == nil {
		clock = time.Now
	}

	// Resolution is keyed by hostname, so recording which literals and
	// which names a node's own ServerAddress contributed is enough to know,
	// after resolving, which addresses are landings. A name that is one
	// node's server and another's relay is a landing: it IS a node.
	literals := map[netip.Addr]struct{}{}
	names := map[string]struct{}{}
	landingLiterals := map[netip.Addr]struct{}{}
	landingNames := map[string]struct{}{}
	collect := func(raw string, landing bool) {
		h := normaliseInfraHost(raw)
		if h == "" {
			return
		}
		if a, err := netip.ParseAddr(h); err == nil {
			a = a.Unmap().WithZone("")
			literals[a] = struct{}{}
			if landing {
				landingLiterals[a] = struct{}{}
			}
			return
		}
		names[h] = struct{}{}
		if landing {
			landingNames[h] = struct{}{}
		}
	}
	for _, n := range nodes {
		if n == nil || n.IsSeparator() || !n.Enabled {
			continue
		}
		collect(n.ServerAddress, true)
		for _, r := range n.Relays {
			if r.Enabled {
				collect(r.Address, false)
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
			results[i] = hostResolution{addrs: addrs, expiresAt: now.Add(ttl)}
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
	landing := make(map[netip.Addr]struct{}, len(landingLiterals)+len(landingNames))
	for a := range landingLiterals {
		landing[a] = struct{}{}
	}
	for h := range landingNames {
		for _, a := range hosts[h].addrs {
			landing[a] = struct{}{}
		}
	}

	c.mu.Lock()
	c.current = current
	c.landing = landing
	c.hosts = hosts
	c.loaded = true
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
