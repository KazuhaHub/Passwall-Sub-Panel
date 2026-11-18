package transit

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// LinkRepo is the link store (sqlstore transit link repo).
type LinkRepo interface {
	EnsureLink(ctx context.Context, entry, exit int64, create func() (domain.TransitLink, error)) (domain.TransitLink, error)
	GetLink(ctx context.Context, entry, exit int64) (domain.TransitLink, error)
	ListLinks(ctx context.Context) ([]domain.TransitLink, error)
	UpdateLinkSettings(ctx context.Context, id int64, settings domain.TransitLinkSettings) error
	RecordLinkSent(ctx context.Context, id int64, side domain.LinkSide, digest string, bound []string) (bool, error)
	SetLinkApplied(ctx context.Context, id int64, side domain.LinkSide, digest string) (bool, error)
	DeleteLink(ctx context.Context, entry, exit int64) (bool, error)
	RecordLinkCounters(ctx context.Context, id int64, side domain.LinkSide, up, down int64, epoch uint64) error
}

// AgentLister finds the agents of servers, for their observed addresses.
type AgentLister interface {
	ListByPanelIDs(ctx context.Context, panelIDs []int64) ([]*domain.NodeAgent, error)
}

// WithLinks enables tunnel lines. Without it the service publishes forwards
// only.
func (s *Service) WithLinks(links LinkRepo, agents AgentLister) *Service {
	s.links, s.agents = links, agents
	return s
}

// RealityCamouflageSites are the defaults a new REALITY link picks from, as a
// REALITY inbound's form starts from one. An administrator can set another.
var RealityCamouflageSites = []string{
	"www.asus.com", "www.samsung.com", "www.nvidia.com", "www.amd.com", "www.intel.com",
}

// Link ports are allocated from this range on the exit server.
const (
	linkPortLow  = 20000
	linkPortHigh = 60000
)

// LinkPlan is one server's share of the tunnels: the halves it holds, which of
// its listeners leave through which link, and, for the policy compile, the exit
// server of each bound node.
type LinkPlan struct {
	In         []nodeprotocol.LinkIn
	Out        []nodeprotocol.LinkOut
	Egress     []nodeprotocol.EgressBinding
	EgressExit map[int64]int64
	// RouteKeys is, on an entry server asked for routes, the link from it to
	// each exit server its nodes' routes name: the key exists as soon as the
	// link row does, sent or not. RouteOut holds the ones whose outgoing half
	// is in Out. A route whose exit is in RouteKeys but not RouteOut cannot be
	// sent: its listener is withheld instead, because sending the route
	// without the link would be refused and dropping it would leave the
	// destinations through the default exit.
	RouteKeys map[int64]nodeprotocol.LinkKey
	RouteOut  map[int64]nodeprotocol.LinkKey
	// Changed reports that what is sent differs from the last sync, which
	// changes which nodes are publishable.
	Changed bool
}

// LinksFor builds the tunnels a server takes part in. occupied are ports this
// server already listens on (its inbounds and forwards); an incoming link on
// one of them is withheld, like a forward, so the node never refuses its whole
// config over one link.
//
// An entry gets its outgoing link and bindings regardless of whether the exit
// has applied its half yet: until it has, the bound users' traffic fails rather
// than leaving from the entry, and the node is withheld from subscriptions by
// readiness. Leaving locally in the meantime would quietly replace the exit the
// administrator chose.
//
// routes says the server asked declared transit.route.v1: only then are the
// links its nodes' routes leave through part of its outgoing half. The exit of
// such a link takes its incoming half whatever the entry declared, so the link
// is ready as soon as the entry can use it.
func (s *Service) LinksFor(ctx context.Context, panelID int64, occupied map[int]bool, routes bool) (LinkPlan, error) {
	return s.LinksForFronts(ctx, panelID, occupied, routes, nil)
}

// LinksForFronts includes incoming links carried by a compiled front.
func (s *Service) LinksForFronts(ctx context.Context, panelID int64, occupied map[int]bool, routes bool, fronted []nodeprotocol.LinkIn) (LinkPlan, error) {
	if s.links == nil {
		return LinkPlan{}, nil
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return LinkPlan{}, fmt.Errorf("transit: list nodes: %w", err)
	}
	type pair struct{ entry, exit int64 }
	bound := map[pair][]*domain.Node{}
	// viaRoute are the pairs some route of an enabled node leaves through.
	viaRoute := map[pair]bool{}
	for _, node := range nodes {
		if node == nil || !node.Enabled || node.IsSeparator() {
			continue
		}
		if node.EgressPanelID != 0 && node.EgressPanelID != node.PanelID && (node.PanelID == panelID || node.EgressPanelID == panelID) {
			key := pair{node.PanelID, node.EgressPanelID}
			bound[key] = append(bound[key], node)
		}
		for _, route := range node.DestRoutes {
			if route.Target.Kind != domain.DestRouteServer || route.Target.PanelID <= 0 || route.Target.PanelID == node.PanelID {
				continue
			}
			if (node.PanelID == panelID && routes) || route.Target.PanelID == panelID {
				if s.panelGone != nil && s.panelGone(ctx, route.Target.PanelID) {
					// A route to a deleted server has no tunnel: its listener is
					// withheld by the routing plan, and none is made or kept for it.
					continue
				}
				viaRoute[pair{node.PanelID, route.Target.PanelID}] = true
			}
		}
	}
	if len(bound) == 0 && len(viaRoute) == 0 {
		return LinkPlan{}, nil
	}
	pairSet := make(map[pair]bool, len(bound)+len(viaRoute))
	for key := range bound {
		pairSet[key] = true
	}
	for key := range viaRoute {
		pairSet[key] = true
	}
	pairs := make([]pair, 0, len(pairSet))
	for key := range pairSet {
		pairs = append(pairs, key)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].entry != pairs[j].entry {
			return pairs[i].entry < pairs[j].entry
		}
		return pairs[i].exit < pairs[j].exit
	})
	exits := make([]int64, 0, len(pairs))
	for _, key := range pairs {
		exits = append(exits, key.exit)
	}
	addresses, err := s.serverAddresses(ctx, exits)
	if err != nil {
		return LinkPlan{}, err
	}
	shared := make(map[int64]map[int64]int)
	if s.frontPorts != nil {
		for _, exit := range exits {
			if _, found := shared[exit]; found {
				continue
			}
			ports, err := s.frontPorts(ctx, exit)
			if err != nil {
				return LinkPlan{}, fmt.Errorf("transit: read front ports for server %d: %w", exit, err)
			}
			shared[exit] = ports
		}
	}
	active := make(map[nodeprotocol.LinkKey]nodeprotocol.LinkIn, len(fronted))
	for _, in := range fronted {
		active[in.Key] = in
	}
	plan := LinkPlan{EgressExit: map[int64]int64{}}
	for _, key := range pairs {
		link, ok, err := s.ensurePairLink(ctx, nodes, key.entry, key.exit)
		if err != nil {
			return LinkPlan{}, fmt.Errorf("transit: link %d→%d: %w", key.entry, key.exit, err)
		}
		if !ok {
			// Nothing uses the pair any more: it was dropped after the nodes were
			// read, and is not made again.
			continue
		}
		if key.entry == panelID && viaRoute[key] {
			if plan.RouteKeys == nil {
				plan.RouteKeys = map[int64]nodeprotocol.LinkKey{}
			}
			plan.RouteKeys[key.exit] = nodeprotocol.NewLinkKey(link.ID)
		}
		port := shared[key.exit][link.ID]
		if key.exit == panelID {
			in := link.LinkIn()
			if port != 0 {
				sent, ok := active[in.Key]
				if !ok || sent.Port != port {
					if err := s.sent(ctx, &plan, link.ID, domain.LinkSideIn, "", nil); err != nil {
						return LinkPlan{}, err
					}
					continue
				}
				in = sent
			}
			if err := nodeprotocol.ValidateLinkIn(in); err != nil {
				log.Warn("tunnel withheld from its exit: its half is invalid", "link", link.ID, "err", err)
				if err := s.sent(ctx, &plan, link.ID, domain.LinkSideIn, "", nil); err != nil {
					return LinkPlan{}, err
				}
				continue
			}
			if port == 0 && occupied[in.Port] {
				log.Warn("tunnel withheld from its exit: its port is taken there", "link", link.ID, "port", in.Port)
				if err := s.sent(ctx, &plan, link.ID, domain.LinkSideIn, "", nil); err != nil {
					return LinkPlan{}, err
				}
				continue
			}
			occupied[in.Port] = true
			if err := s.sent(ctx, &plan, link.ID, domain.LinkSideIn, domain.TransitLinkDigest(in, nil), nil); err != nil {
				return LinkPlan{}, err
			}
			plan.In = append(plan.In, in)
			continue
		}
		address := domain.TransitLinkAddress(link, addresses[key.exit])
		out := link.LinkOut(address)
		if port != 0 {
			out.Port = port
		}
		if err := nodeprotocol.ValidateLinkOut(out); err != nil {
			log.Warn("tunnel withheld from its entry: no usable exit address", "link", link.ID, "err", err)
			if err := s.sent(ctx, &plan, link.ID, domain.LinkSideOut, "", nil); err != nil {
				return LinkPlan{}, err
			}
			continue
		}
		var listeners []nodeprotocol.ListenerKey
		for _, node := range bound[key] {
			listeners = append(listeners, nodeprotocol.NewListenerKey(node.ID))
		}
		sort.Slice(listeners, func(i, j int) bool { return listeners[i] < listeners[j] })
		names := make([]string, 0, len(listeners))
		for _, listener := range listeners {
			names = append(names, string(listener))
			plan.Egress = append(plan.Egress, nodeprotocol.EgressBinding{Listener: listener, Link: out.Key})
		}
		if err := s.sent(ctx, &plan, link.ID, domain.LinkSideOut, domain.TransitLinkDigest(out, listeners), names); err != nil {
			return LinkPlan{}, err
		}
		for _, node := range bound[key] {
			plan.EgressExit[node.ID] = key.exit
		}
		plan.Out = append(plan.Out, out)
		if viaRoute[key] {
			if plan.RouteOut == nil {
				plan.RouteOut = map[int64]nodeprotocol.LinkKey{}
			}
			plan.RouteOut[key.exit] = out.Key
		}
	}
	return plan, nil
}

// ensurePairLink returns the link of a pair, making it when it does not exist.
// Making one happens under the locks of both servers, and only if a node of the
// entry still uses the pair at that moment: a save that has just dropped the
// tunnel (it removes it under the same locks) must not see it come back because
// this sync read the nodes a moment earlier.
func (s *Service) ensurePairLink(ctx context.Context, nodes []*domain.Node, entry, exit int64) (domain.TransitLink, bool, error) {
	if link, err := s.links.GetLink(ctx, entry, exit); err == nil {
		return link, true, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.TransitLink{}, false, err
	}
	defer s.lockPair(entry, exit)()
	if s.serverLock != nil {
		fresh, err := s.nodes.List(ctx)
		if err != nil {
			return domain.TransitLink{}, false, err
		}
		nodes = fresh
		used := false
		for _, node := range nodes {
			if node == nil || node.PanelID != entry || !node.Enabled || node.IsSeparator() {
				continue
			}
			if node.EgressPanelID == exit {
				used = true
			}
			for _, route := range node.DestRoutes {
				used = used || (route.Target.Kind == domain.DestRouteServer && route.Target.PanelID == exit)
			}
		}
		if !used {
			return domain.TransitLink{}, false, nil
		}
	}
	link, err := s.links.EnsureLink(ctx, entry, exit, func() (domain.TransitLink, error) {
		return s.newLink(ctx, nodes, entry, exit)
	})
	return link, err == nil, err
}

// sent records what one side of a link is being sent; "" records that it is
// withheld, so an earlier confirmation cannot keep its nodes published.
func (s *Service) sent(ctx context.Context, plan *LinkPlan, id int64, side domain.LinkSide, digest string, bound []string) error {
	changed, err := s.links.RecordLinkSent(ctx, id, side, digest, bound)
	if err != nil {
		return fmt.Errorf("transit: record link %d sent: %w", id, err)
	}
	plan.Changed = plan.Changed || changed
	return nil
}

// ConfirmLinks records what a server confirmed of the links in the config it
// holds, per side. It reports whether anything changed.
func (s *Service) ConfirmLinks(ctx context.Context, held *nodeprotocol.Transit, statuses map[string]nodeprotocol.ObjectStatus) (bool, error) {
	if s.links == nil || held == nil {
		return false, nil
	}
	changed := false
	record := func(key nodeprotocol.LinkKey, side domain.LinkSide, digest string) error {
		id, err := key.RowID()
		if err != nil {
			return nil
		}
		if status, ok := statuses[string(key)]; !ok || status.State != nodeprotocol.ObjectApplied {
			digest = ""
		}
		moved, err := s.links.SetLinkApplied(ctx, id, side, digest)
		changed = changed || moved
		return err
	}
	for _, in := range held.LinksIn {
		if err := record(in.Key, domain.LinkSideIn, domain.TransitLinkDigest(in, nil)); err != nil {
			return changed, fmt.Errorf("transit: confirm %s: %w", in.Key, err)
		}
	}
	for _, out := range held.LinksOut {
		var listeners []nodeprotocol.ListenerKey
		for _, binding := range held.Egress {
			if binding.Link == out.Key {
				listeners = append(listeners, binding.Listener)
			}
		}
		if err := record(out.Key, domain.LinkSideOut, domain.TransitLinkDigest(out, listeners)); err != nil {
			return changed, fmt.Errorf("transit: confirm %s: %w", out.Key, err)
		}
	}
	return changed, nil
}

// RecordLinkCounters books a server's link counters on the side it holds each
// link, accepting only links in the config the panel sent it.
func (s *Service) RecordLinkCounters(ctx context.Context, sent *nodeprotocol.Transit, counters []nodeprotocol.LinkCounters) error {
	if s.links == nil || sent == nil || len(counters) == 0 {
		return nil
	}
	side := map[nodeprotocol.LinkKey]domain.LinkSide{}
	for _, in := range sent.LinksIn {
		side[in.Key] = domain.LinkSideIn
	}
	for _, out := range sent.LinksOut {
		side[out.Key] = domain.LinkSideOut
	}
	for _, counter := range counters {
		which, owned := side[counter.Key]
		if !owned || !counter.Present {
			continue
		}
		id, err := counter.Key.RowID()
		if err != nil {
			continue
		}
		if err := s.links.RecordLinkCounters(ctx, id, which, counter.UpBytes, counter.DownBytes, counter.CounterEpoch); err != nil {
			return fmt.Errorf("transit: record %s counters: %w", counter.Key, err)
		}
	}
	return nil
}

// EnsureLink makes sure the tunnel from entry to exit exists, with fresh
// credentials and a port on the exit when it does not, and returns it. A route
// that leaves through another server asks for it when it is saved, so the
// administrator sees the link before the first sync; the sync creates it the
// same way when it is missing.
func (s *Service) EnsureLink(ctx context.Context, entry, exit int64) (domain.TransitLink, error) {
	if s.links == nil {
		return domain.TransitLink{}, errors.New("transit: tunnels are not enabled")
	}
	if entry <= 0 || exit <= 0 || entry == exit {
		return domain.TransitLink{}, fmt.Errorf("%w: a tunnel joins two different servers", domain.ErrValidation)
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return domain.TransitLink{}, fmt.Errorf("transit: list nodes: %w", err)
	}
	return s.links.EnsureLink(ctx, entry, exit, func() (domain.TransitLink, error) {
		return s.newLink(ctx, nodes, entry, exit)
	})
}

// OutUsable says whether the entry's half of the link to exit can be sent: an
// address to dial exists (the link's own, else the one the exit's sync came
// from) and the half passes the wire contract. A link that does not exist yet is
// judged with the credentials it would be made with.
func (s *Service) OutUsable(ctx context.Context, entry, exit int64) (bool, error) {
	if s.links == nil {
		return false, nil
	}
	link, err := s.links.GetLink(ctx, entry, exit)
	if errors.Is(err, domain.ErrNotFound) {
		link = domain.TransitLink{ID: 1, Protocol: nodeprotocol.LinkProtocolSS2022, Port: 20000,
			SSMethod: "2022-blake3-aes-128-gcm", SSKey: "AAAAAAAAAAAAAAAAAAAAAA=="}
	} else if err != nil {
		return false, err
	}
	addresses, err := s.serverAddresses(ctx, []int64{exit})
	if err != nil {
		return false, err
	}
	out := link.LinkOut(domain.TransitLinkAddress(link, addresses[exit]))
	return nodeprotocol.ValidateLinkOut(out) == nil, nil
}

// RemoveLink deletes the tunnel from entry to exit and frees its port. The
// caller has checked that nothing uses the pair any more: a node's default exit
// and every route leaving through it.
func (s *Service) RemoveLink(ctx context.Context, entry, exit int64) error {
	if s.links == nil {
		return nil
	}
	_, err := s.links.DeleteLink(ctx, entry, exit)
	return err
}

// LinkRow returns the link from entry to exit as stored, domain.ErrNotFound
// when none exists.
func (s *Service) LinkRow(ctx context.Context, entry, exit int64) (domain.TransitLink, error) {
	if s.links == nil {
		return domain.TransitLink{}, domain.ErrNotFound
	}
	return s.links.GetLink(ctx, entry, exit)
}

// ServerAddress is the address PSP last saw a server's sync come from, or "".
func (s *Service) ServerAddress(ctx context.Context, panelID int64) (string, error) {
	if s.agents == nil {
		return "", nil
	}
	agents, err := s.agents.ListByPanelIDs(ctx, []int64{panelID})
	if err != nil || len(agents) == 0 {
		return "", err
	}
	return agents[0].ObservedAddress, nil
}

// serverAddresses maps each Passwall Node server to the address PSP last saw
// its sync come from.
func (s *Service) serverAddresses(ctx context.Context, panelIDs []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if s.agents == nil {
		return out, nil
	}
	agents, err := s.agents.ListByPanelIDs(ctx, panelIDs)
	if err != nil {
		return nil, err
	}
	for _, agent := range agents {
		if agent != nil {
			out[agent.PanelID] = agent.ObservedAddress
		}
	}
	return out, nil
}

// ErrReservedPortTaken is returned when the port promised to a new link was
// taken by something else before the link was made: the link is not made on
// another port behind the back of a confirmation that listed this one.
var ErrReservedPortTaken = fmt.Errorf("%w: the port planned for the tunnel was taken: plan it again", domain.ErrConflict)

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// liveReservations are the ports promised on an exit server that have not
// expired (expired ones are forgotten).
func (s *Service) liveReservations(exit int64) map[int]bool {
	s.reservedMu.Lock()
	defer s.reservedMu.Unlock()
	live := map[int]bool{}
	for key, r := range s.reserved {
		if !r.expires.After(s.clock()) {
			delete(s.reserved, key)
			continue
		}
		if key[1] == exit {
			live[r.port] = true
		}
	}
	return live
}

// ReservedPorts are the link ports promised on a server and still live. Every
// allocator of ports on a server reads them, so that none hands one out.
func (s *Service) ReservedPorts(exit int64) []int {
	live := s.liveReservations(exit)
	out := make([]int, 0, len(live))
	for port := range live {
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}

func (s *Service) takeReservation(key [2]int64) (reservation, bool) {
	s.reservedMu.Lock()
	defer s.reservedMu.Unlock()
	r, had := s.reserved[key]
	delete(s.reserved, key)
	return r, had
}

func (s *Service) putReservation(key [2]int64, r reservation) {
	s.reservedMu.Lock()
	defer s.reservedMu.Unlock()
	if s.reserved == nil {
		s.reserved = map[[2]int64]reservation{}
	}
	s.reserved[key] = r
}

// PlanLink says which port on the exit server the tunnel from entry uses: the
// existing link's, or the one a new link will be made with. Making it reserves
// the port for the pair for ten minutes, so that a confirmation that lists the
// port is followed by a link that has it; the reservation is honoured by every
// other link's allocation. The caller holds the exit server's lock (a node save
// does), which makes the choice and the use of the port one critical section.
func (s *Service) PlanLink(ctx context.Context, entry, exit int64) (int, bool, error) {
	return s.PlanLinkPort(ctx, entry, exit, 0)
}

// PlanLinkPort reserves the requested port for a new link, or chooses one
// when requested is zero. Occupied ports may include other listeners in the
// same setup that have yet to be saved. Existing links keep their port.
func (s *Service) PlanLinkPort(ctx context.Context, entry, exit int64, requested int, occupied ...int) (int, bool, error) {
	if s.links == nil {
		return 0, false, errors.New("transit: tunnels are not enabled")
	}
	if link, err := s.links.GetLink(ctx, entry, exit); err == nil {
		if requested != 0 && link.Port != requested {
			return 0, true, fmt.Errorf("%w: the transit tunnel from server %d to server %d already uses port %d", domain.ErrConflict, entry, exit, link.Port)
		}
		return link.Port, true, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return 0, false, err
	}
	if requested < 0 || requested > 65535 {
		return 0, false, fmt.Errorf("%w: port %d is out of range", domain.ErrValidation, requested)
	}
	if owner, reserved := domain.NodeReservedPort(requested); reserved {
		return 0, false, fmt.Errorf("%w: port %d is reserved for the %s on server %d", domain.ErrValidation, requested, owner, exit)
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return 0, false, err
	}
	s.allocMu.Lock()
	defer s.allocMu.Unlock()
	key := [2]int64{entry, exit}
	r, had := s.takeReservation(key) // so that its own promise does not count against it
	if requested != 0 {
		if slices.Contains(occupied, requested) || !s.portFree(ctx, nodes, exit, requested) {
			if had {
				s.putReservation(key, r)
			}
			return 0, false, fmt.Errorf("%w: transit port %d is unavailable on server %d", domain.ErrValidation, requested, exit)
		}
		s.putReservation(key, reservation{port: requested, expires: s.clock().Add(reservationTTL)})
		return requested, false, nil
	}
	if had && r.expires.After(s.clock()) && !slices.Contains(occupied, r.port) && s.portFree(ctx, nodes, exit, r.port) {
		// Seen again, the same promise is kept (and its ten minutes begin anew).
		s.putReservation(key, reservation{port: r.port, expires: s.clock().Add(reservationTTL)})
		return r.port, false, nil
	}
	port, err := s.freeExitPortLocked(ctx, nodes, exit, occupied...)
	if err != nil {
		return 0, false, err
	}
	s.putReservation(key, reservation{port: port, expires: s.clock().Add(reservationTTL)})
	return port, false, nil
}

// portFree says nothing holds the port on the exit server: its inbounds,
// forwards entering it, other links, the fronts' ports and loopbacks, and the
// other live reservations. The caller holds allocMu.
func (s *Service) portFree(ctx context.Context, nodes []*domain.Node, exit int64, port int) bool {
	if s.liveReservations(exit)[port] {
		return false
	}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if node.PanelID == exit && node.DesiredPort == port {
			return false
		}
		for _, relay := range node.Relays {
			if desired, ok := domain.DesiredManagedForward(node, relay); ok && desired.EntryPanelID == exit && desired.Port == port {
				return false
			}
		}
	}
	if links, err := s.links.ListLinks(ctx); err != nil {
		return false
	} else {
		for _, link := range links {
			if link.ExitPanelID == exit && link.Port == port {
				return false
			}
		}
	}
	return s.portInUse == nil || !s.portInUse(ctx, exit, port)
}

// reservedPort takes the port PlanLink reserved for a pair, and reports whether
// there was one. found with ok false means it was promised and has been taken
// since.
func (s *Service) reservedPort(ctx context.Context, nodes []*domain.Node, entry, exit int64) (port int, found, ok bool) {
	s.allocMu.Lock()
	defer s.allocMu.Unlock()
	r, has := s.takeReservation([2]int64{entry, exit})
	if !has || !r.expires.After(s.clock()) {
		return 0, false, false
	}
	// Its own reservation is already gone, so portFree sees only the others.
	return r.port, true, s.portFree(ctx, nodes, exit, r.port)
}

// newLink creates a link with fresh credentials for both protocols (so an
// administrator can switch protocol without new secrets), a free port on the
// exit and a camouflage site drawn from the defaults.
func (s *Service) newLink(ctx context.Context, nodes []*domain.Node, entry, exit int64) (domain.TransitLink, error) {
	port, promised, usable := s.reservedPort(ctx, nodes, entry, exit)
	switch {
	case promised && !usable:
		return domain.TransitLink{}, ErrReservedPortTaken
	case !promised:
		var err error
		if port, err = s.freeExitPort(ctx, nodes, exit); err != nil {
			return domain.TransitLink{}, err
		}
	}
	ssKey := make([]byte, 16)
	if _, err := rand.Read(ssKey); err != nil {
		return domain.TransitLink{}, err
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return domain.TransitLink{}, err
	}
	shortID := make([]byte, 4)
	if _, err := rand.Read(shortID); err != nil {
		return domain.TransitLink{}, err
	}
	pick, err := rand.Int(rand.Reader, big.NewInt(int64(len(RealityCamouflageSites))))
	if err != nil {
		return domain.TransitLink{}, err
	}
	site := RealityCamouflageSites[pick.Int64()]
	return domain.TransitLink{
		EntryPanelID: entry, ExitPanelID: exit, Protocol: nodeprotocol.LinkProtocolSS2022, Port: port,
		SSMethod: "2022-blake3-aes-128-gcm", SSKey: base64.StdEncoding.EncodeToString(ssKey),
		RealityUUID:       uuid.NewString(),
		RealityPrivateKey: base64.RawURLEncoding.EncodeToString(private.Bytes()),
		RealityPublicKey:  base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()),
		RealityShortID:    hex.EncodeToString(shortID),
		RealityServerName: site, RealityTarget: site + ":443",
	}, nil
}

// freeExitPort picks a random port in the link range that nothing on the exit
// server uses: its inbounds, forwards entering it, and its other links.
func (s *Service) freeExitPort(ctx context.Context, nodes []*domain.Node, exit int64) (int, error) {
	s.allocMu.Lock()
	defer s.allocMu.Unlock()
	return s.freeExitPortLocked(ctx, nodes, exit)
}

// freeExitPortLocked is freeExitPort for a caller holding allocMu. The live
// reservations of other links on the server count as used, and so does anything
// the port check names (shared ports, loopbacks).
func (s *Service) freeExitPortLocked(ctx context.Context, nodes []*domain.Node, exit int64, occupied ...int) (int, error) {
	used := s.liveReservations(exit)
	for _, port := range occupied {
		used[port] = true
	}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if node.PanelID == exit && node.DesiredPort > 0 {
			used[node.DesiredPort] = true
		}
		for _, relay := range node.Relays {
			if desired, ok := domain.DesiredManagedForward(node, relay); ok && desired.EntryPanelID == exit {
				used[desired.Port] = true
			}
		}
	}
	links, err := s.links.ListLinks(ctx)
	if err != nil {
		return 0, err
	}
	for _, link := range links {
		if link.ExitPanelID == exit {
			used[link.Port] = true
		}
	}
	for attempt := 0; attempt < 256; attempt++ {
		n, err := rand.Int(rand.Reader, big.NewInt(linkPortHigh-linkPortLow))
		if err != nil {
			return 0, err
		}
		if port := linkPortLow + int(n.Int64()); !used[port] && (s.portInUse == nil || !s.portInUse(ctx, exit, port)) {
			return port, nil
		}
	}
	return 0, errors.New("no free port for a link on the exit server")
}

// LinkView is a link as the administrator sees it: its settings, never its
// credentials. Exists is false for a pair no node uses yet; the settings are
// then the defaults a new link would start from.
type LinkView struct {
	Exists            bool
	Protocol          string
	ExitAddress       string
	RealityServerName string
	RealityTarget     string
	Port              int
	// ServerAddress is where PSP last saw the exit server's sync come from:
	// the address used when ExitAddress is empty.
	ServerAddress string
	UpBytes       int64
	DownBytes     int64
}

// Link returns the link from entry to exit as the administrator sees it.
func (s *Service) Link(ctx context.Context, entry, exit int64) (LinkView, error) {
	if s.links == nil {
		return LinkView{}, errors.New("transit: tunnels are not enabled")
	}
	address, err := s.ServerAddress(ctx, exit)
	if err != nil {
		return LinkView{}, err
	}
	link, err := s.links.GetLink(ctx, entry, exit)
	if errors.Is(err, domain.ErrNotFound) {
		return LinkView{Protocol: nodeprotocol.LinkProtocolSS2022, ServerAddress: address}, nil
	}
	if err != nil {
		return LinkView{}, err
	}
	// Both sides meter the same traffic (an Xray and a sing-box server both
	// do): the entry's figure is used, the exit's only while the entry has
	// reported none.
	up, down := link.OutUpBytes, link.OutDownBytes
	if up == 0 && down == 0 {
		up, down = link.InUpBytes, link.InDownBytes
	}
	return LinkView{
		Exists: true, Protocol: link.Protocol, ExitAddress: link.ExitAddress,
		RealityServerName: link.RealityServerName, RealityTarget: link.RealityTarget,
		Port: link.Port, ServerAddress: address, UpBytes: up, DownBytes: down,
	}, nil
}

// UpdateLink applies an administrator's settings to the link from entry to
// exit, creating it if no node uses it yet. Empty REALITY fields keep the
// link's camouflage site.
func (s *Service) UpdateLink(ctx context.Context, entry, exit int64, settings domain.TransitLinkSettings) (LinkView, error) {
	if s.links == nil {
		return LinkView{}, errors.New("transit: tunnels are not enabled")
	}
	if entry <= 0 || exit <= 0 || entry == exit {
		return LinkView{}, fmt.Errorf("%w: a tunnel joins two different servers", domain.ErrValidation)
	}
	defer s.lockPair(entry, exit)()
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return LinkView{}, fmt.Errorf("transit: list nodes: %w", err)
	}
	link, err := s.links.EnsureLink(ctx, entry, exit, func() (domain.TransitLink, error) {
		return s.newLink(ctx, nodes, entry, exit)
	})
	if err != nil {
		return LinkView{}, err
	}
	settings.ExitAddress = strings.TrimSpace(settings.ExitAddress)
	if settings.RealityServerName == "" && settings.RealityTarget == "" {
		settings.RealityServerName, settings.RealityTarget = link.RealityServerName, link.RealityTarget
	} else if settings.RealityTarget == "" {
		settings.RealityTarget = settings.RealityServerName + ":443"
	}
	if err := domain.ValidateTransitLinkSettings(settings); err != nil {
		return LinkView{}, err
	}
	if s.frontPorts != nil && (settings.Protocol != link.Protocol || settings.RealityServerName != link.RealityServerName) {
		ports, err := s.frontPorts(ctx, exit)
		if err != nil {
			return LinkView{}, err
		}
		if port := ports[link.ID]; port != 0 {
			return LinkView{}, fmt.Errorf("%w: tunnel %d uses shared port %d; remove its SNI route before changing its protocol or server name", domain.ErrConflict, link.ID, port)
		}
	}
	if err := s.links.UpdateLinkSettings(ctx, link.ID, settings); err != nil {
		return LinkView{}, err
	}
	return s.Link(ctx, entry, exit)
}

// WithholdIncomingLinks clears readiness for incoming links dropped during
// front validation.
func (s *Service) WithholdIncomingLinks(ctx context.Context, links []nodeprotocol.LinkKey) error {
	var plan LinkPlan
	for _, key := range links {
		id, err := key.RowID()
		if err != nil {
			return err
		}
		if err := s.sent(ctx, &plan, id, domain.LinkSideIn, "", nil); err != nil {
			return err
		}
	}
	return nil
}
