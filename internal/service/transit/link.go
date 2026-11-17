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
	"sort"
	"strings"

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
func (s *Service) LinksFor(ctx context.Context, panelID int64, occupied map[int]bool) (LinkPlan, error) {
	if s.links == nil {
		return LinkPlan{}, nil
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return LinkPlan{}, fmt.Errorf("transit: list nodes: %w", err)
	}
	type pair struct{ entry, exit int64 }
	bound := map[pair][]*domain.Node{}
	for _, node := range nodes {
		if node == nil || node.EgressPanelID == 0 || !node.Enabled || node.IsSeparator() || node.EgressPanelID == node.PanelID {
			continue
		}
		if node.PanelID != panelID && node.EgressPanelID != panelID {
			continue
		}
		key := pair{node.PanelID, node.EgressPanelID}
		bound[key] = append(bound[key], node)
	}
	if len(bound) == 0 {
		return LinkPlan{}, nil
	}
	pairs := make([]pair, 0, len(bound))
	for key := range bound {
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
	plan := LinkPlan{EgressExit: map[int64]int64{}}
	for _, key := range pairs {
		link, err := s.links.EnsureLink(ctx, key.entry, key.exit, func() (domain.TransitLink, error) {
			return s.newLink(ctx, nodes, key.entry, key.exit)
		})
		if err != nil {
			return LinkPlan{}, fmt.Errorf("transit: link %d→%d: %w", key.entry, key.exit, err)
		}
		if key.exit == panelID {
			in := link.LinkIn()
			if err := nodeprotocol.ValidateLinkIn(in); err != nil {
				log.Warn("tunnel withheld from its exit: its half is invalid", "link", link.ID, "err", err)
				if err := s.sent(ctx, &plan, link.ID, domain.LinkSideIn, "", nil); err != nil {
					return LinkPlan{}, err
				}
				continue
			}
			if occupied[in.Port] {
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
	}
	return plan, nil
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

// newLink creates a link with fresh credentials for both protocols (so an
// administrator can switch protocol without new secrets), a free port on the
// exit and a camouflage site drawn from the defaults.
func (s *Service) newLink(ctx context.Context, nodes []*domain.Node, entry, exit int64) (domain.TransitLink, error) {
	port, err := s.freeExitPort(ctx, nodes, exit)
	if err != nil {
		return domain.TransitLink{}, err
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
	used := map[int]bool{}
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
		if port := linkPortLow + int(n.Int64()); !used[port] {
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
	if err := s.links.UpdateLinkSettings(ctx, link.ID, settings); err != nil {
		return LinkView{}, err
	}
	return s.Link(ctx, entry, exit)
}
