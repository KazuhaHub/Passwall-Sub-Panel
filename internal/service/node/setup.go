package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/inboundcfg"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/route"
)

// SetupOptions are the explicit listener settings chosen in the creation
// wizard. There is at most one new listener per server in a setup.
type SetupOptions struct {
	Loopbacks map[int64]int        `json:"loopbacks"`
	Tunnels   map[int64]SetupEntry `json:"tunnels"`
	LinkPorts map[string]int       `json:"link_ports"` // keyed by "<entry>/<exit>"
	Confirm   string               `json:"confirm"`
	Routes    []domain.DestRoute   `json:"routes"`
}

type SetupEntry struct {
	Listen string `json:"listen"`
	SNI    string `json:"sni"`
	Target string `json:"target"`
}

type SetupBinding struct {
	PanelID  int64    `json:"panel_id"`
	PeerID   int64    `json:"peer_id,omitempty"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Address  string   `json:"address,omitempty"`
	Port     int      `json:"port"`
	Listen   string   `json:"listen,omitempty"`
	Loopback int      `json:"loopback,omitempty"`
	FrontID  int64    `json:"front_id,omitempty"`
	SNI      []string `json:"sni,omitempty"`
	Target   string   `json:"target,omitempty"`
	Reused   bool     `json:"reused,omitempty"`
}

type SetupPlan struct {
	Bindings []SetupBinding `json:"bindings"`
	Token    string         `json:"token"`
	Warnings []string       `json:"warnings,omitempty"`
}

type setupListener struct {
	node    *domain.Node
	spec    ports.InboundSpec
	front   *frontChange
	binding SetupBinding
	index   int
}

type setupForward struct {
	frontID int64
	index   int
}

type setupWork struct {
	SetupPlan
	listeners []setupListener
	links     []SetupBinding
	forwards  []setupForward
	routes    []domain.DestRoute
}

type SetupRouter interface {
	CheckSetup(context.Context, *domain.Node, []domain.DestRoute) (route.Saved, error)
	StoreSetup(context.Context, int64, []domain.DestRoute) error
}

func (s *Service) SetSetupRouter(router SetupRouter) { s.setupRouter = router }

// SetupInbound previews a new node or applies the confirmed plan. All port
// ownership checks are rerun under the same server locks as ordinary saves.
func (s *Service) SetupInbound(ctx context.Context, n *domain.Node, spec ports.InboundSpec, opts SetupOptions, preview bool) (SetupPlan, error) {
	panels := append(relayPanels(n), n.EgressPanelID)
	if len(opts.Routes) > 0 {
		defer s.lockRoutes(n.PanelID)()
		for _, r := range opts.Routes {
			panels = append(panels, r.Target.PanelID)
		}
	}
	defer s.lockFronts(panels...)()
	work, err := s.planSetup(ctx, n, spec, opts)
	if err != nil {
		return SetupPlan{}, err
	}
	if preview {
		return work.SetupPlan, nil
	}
	if opts.Confirm == "" || opts.Confirm != work.Token {
		return SetupPlan{}, fmt.Errorf("%w: the node setup plan changed; review the ports again", domain.ErrConflict)
	}
	return work.SetupPlan, s.applySetup(ctx, n, work)
}

func (s *Service) planSetup(ctx context.Context, n *domain.Node, spec ports.InboundSpec, opts SetupOptions) (*setupWork, error) {
	if err := domain.ReconcileRelayIDs(nil, n.Relays); err != nil {
		return nil, err
	}
	inboundcfg.ApplySpec(n, spec)
	n.Enabled = spec.Enable
	if err := s.validateEgress(n, n.EgressPanelID); err != nil {
		return nil, err
	}
	if err := resolveSourceCIDRs(nil, n); err != nil {
		return nil, err
	}
	if err := s.checkSourceGuard(ctx, nil, n); err != nil {
		return nil, err
	}
	if err := domain.ValidateTunnelRelays(n, n.Relays); err != nil {
		return nil, err
	}
	if err := settleCDN(nil, n, n.Relays); err != nil {
		return nil, err
	}
	if err := s.validateManagedRelays(ctx, n); err != nil {
		return nil, err
	}
	domain.ResolveRelayLabels(nil, n.Relays, n.Region, n.Tags)
	landing, err := s.planSetupListener(ctx, n, spec, opts.Loopbacks[n.PanelID], "landing")
	if err != nil {
		return nil, err
	}
	work := &setupWork{listeners: []setupListener{landing}}
	work.Bindings = append(work.Bindings, landing.binding)
	seen := map[int64]bool{n.PanelID: true}
	for i, line := range n.Relays {
		if line.EntryPanelID == 0 {
			continue
		}
		if seen[line.EntryPanelID] {
			return nil, fmt.Errorf("%w: choose one entry per server in node setup (server %d)", domain.ErrValidation, line.EntryPanelID)
		}
		seen[line.EntryPanelID] = true
		if !line.Tunnel() {
			var frontID int64
			if s.frontSvc != nil {
				frontID, err = s.frontSvc.SetupForward(ctx, n, line, true)
				if err != nil {
					return nil, err
				}
				if frontID != 0 {
					work.forwards = append(work.forwards, setupForward{frontID: frontID, index: i})
				}
			}
			if frontID == 0 {
				if err := s.checkTunnelPort(ctx, line.EntryPanelID, line.Port, portOwner{}); err != nil {
					return nil, err
				}
			}
			work.Bindings = append(work.Bindings, SetupBinding{PanelID: line.EntryPanelID, Kind: "forward", Name: line.Label(), Port: line.Port, Address: line.Address,
				FrontID: frontID, SNI: domain.LandingOfRelay(n).ServerNames})
			continue
		}
		if err := s.validateEgress(&domain.Node{PanelID: line.EntryPanelID}, n.PanelID); err != nil {
			return nil, err
		}
		entry := opts.Tunnels[line.EntryPanelID]
		if strings.TrimSpace(entry.SNI) == "" || strings.TrimSpace(entry.Target) == "" {
			return nil, fmt.Errorf("%w: tunnel entry %q needs an SNI and a target", domain.ErrValidation, line.Label())
		}
		name := domain.TunnelLineLabel(n.DisplayName, line.Label())
		settings, err := newTunnelSettings(n, name, line.Port, entry.SNI, entry.Target)
		if err != nil {
			return nil, err
		}
		settings.Spec.Listen = entry.Listen
		child := &domain.Node{
			PanelID: line.EntryPanelID, DisplayName: name, ServerAddress: line.Address,
			Flow: settings.Flow, Region: n.Region, Tags: domain.CopyLineTags(n.Tags),
			SortOrder: n.SortOrder, Kind: domain.NodeKindReal, TrafficFactorMilli: line.TrafficFactorMilli,
			EgressPanelID: n.PanelID,
		}
		inboundcfg.ApplySpec(child, settings.Spec)
		listener, err := s.planSetupListener(ctx, child, settings.Spec, opts.Loopbacks[child.PanelID], "tunnel")
		if err != nil {
			return nil, err
		}
		listener.index = i
		work.listeners = append(work.listeners, listener)
		work.Bindings = append(work.Bindings, listener.binding)
	}
	for _, listener := range work.listeners[1:] {
		if err := s.planSetupLink(ctx, work, listener.node.PanelID, n.PanelID, opts.LinkPorts); err != nil {
			return nil, err
		}
	}
	if n.EgressPanelID != 0 {
		if err := s.planSetupLink(ctx, work, n.PanelID, n.EgressPanelID, opts.LinkPorts); err != nil {
			return nil, err
		}
	}
	if len(opts.Routes) > 0 {
		if s.setupRouter == nil {
			return nil, fmt.Errorf("%w: destination routing is unavailable", domain.ErrValidation)
		}
		checked, err := s.setupRouter.CheckSetup(ctx, n, opts.Routes)
		if err != nil {
			return nil, err
		}
		work.routes = checked.Routes
		for _, w := range checked.Warnings {
			work.Warnings = append(work.Warnings, w.Message)
		}
		for _, r := range checked.Routes {
			if r.Target.Kind == domain.DestRouteServer {
				if err := s.planSetupLink(ctx, work, n.PanelID, r.Target.PanelID, opts.LinkPorts); err != nil {
					return nil, err
				}
			}
		}
	}
	// Tokens bind the reviewed configuration and allocated ports. Newly
	// generated tunnel credentials are intentionally absent from the preview.
	lines := slices.Clone(n.Relays)
	for i := range lines {
		lines[i].ID = ""
	}
	b, err := json.Marshal([]any{work.Bindings, lines, n.HideDirect, n.EgressPanelID, n.Flow, n.Region, n.Tags, spec, opts.Tunnels, work.routes})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(b)
	work.Token = hex.EncodeToString(digest[:])
	return work, nil
}

func (s *Service) planSetupLink(ctx context.Context, work *setupWork, entry, exit int64, linkPorts map[string]int) error {
	for _, link := range work.links {
		if link.PeerID == entry && link.PanelID == exit {
			return nil
		}
	}
	if s.tunnelLinks == nil {
		return fmt.Errorf("%w: transit tunnels are unavailable", domain.ErrValidation)
	}
	requested := linkPorts[fmt.Sprintf("%d/%d", entry, exit)]
	var occupied []int
	for _, b := range work.Bindings {
		if b.PanelID != exit {
			continue
		}
		if requested != 0 && (b.Port == requested || b.Loopback == requested) {
			return setupLinkConflict(b, requested)
		}
		occupied = append(occupied, b.Port, b.Loopback)
	}
	port, exists, err := s.tunnelLinks.PlanLinkPort(ctx, entry, exit, requested, occupied...)
	if err != nil {
		return err
	}
	if exists {
		for _, b := range work.Bindings {
			if b.PanelID == exit && (b.Port == port || b.Loopback == port) {
				return setupLinkConflict(b, port)
			}
		}
	}
	b := SetupBinding{PanelID: exit, PeerID: entry, Kind: "link", Port: port, Reused: exists}
	work.links = append(work.links, b)
	work.Bindings = append(work.Bindings, b)
	return nil
}

func setupLinkConflict(binding SetupBinding, port int) error {
	owner := binding.Name
	if binding.Kind == "link" {
		owner = fmt.Sprintf("transit tunnel from server %d", binding.PeerID)
	}
	return fmt.Errorf("%w: port %d on server %d conflicts with %q", domain.ErrValidation, port, binding.PanelID, owner)
}

func (s *Service) planSetupListener(ctx context.Context, n *domain.Node, spec ports.InboundSpec, loopback int, kind string) (setupListener, error) {
	result := setupListener{node: n, spec: spec}
	if spec.Listen != "" && net.ParseIP(spec.Listen) == nil {
		return result, fmt.Errorf("%w: listener %q needs an IP address for its bind", domain.ErrValidation, n.DisplayName)
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return result, fmt.Errorf("%w: port %d is out of range", domain.ErrValidation, spec.Port)
	}
	client, err := s.pool.Get(n.PanelID)
	if err != nil {
		return result, err
	}
	if async, ok := client.(ports.AsynchronousApplier); !ok || !async.ApplyIsAsynchronous() {
		inbounds, err := client.ListInbounds(ctx)
		if err != nil {
			return result, err
		}
		for _, other := range inbounds {
			if other.Port == spec.Port {
				return result, fmt.Errorf("%w: port %d on server %d is used by inbound %q", domain.ErrValidation, spec.Port, n.PanelID, other.Remark)
			}
		}
	}
	b := SetupBinding{PanelID: n.PanelID, Kind: kind, Name: n.DisplayName, Address: n.ServerAddress, Port: spec.Port, Listen: spec.Listen}
	var stream struct {
		Reality struct {
			Names  []string `json:"serverNames"`
			Target string   `json:"target"`
			Dest   string   `json:"dest"`
		} `json:"realitySettings"`
		TLS struct {
			Name string `json:"serverName"`
		} `json:"tlsSettings"`
	}
	if json.Unmarshal([]byte(spec.StreamSettings), &stream) == nil {
		b.SNI = stream.Reality.Names
		b.Target = firstText(stream.Reality.Target, stream.Reality.Dest)
		if len(b.SNI) == 0 && stream.TLS.Name != "" {
			b.SNI = []string{stream.TLS.Name}
		}
	}
	if s.frontSvc != nil {
		offer, err := s.frontSvc.JoinOffer(ctx, n)
		if err != nil {
			return result, err
		}
		if offer != nil {
			if loopback == 0 {
				loopback = offer.Loopback
			}
			if err := s.frontSvc.JoinCheck(ctx, n, offer, loopback); err != nil {
				return result, err
			}
			b.Loopback, b.FrontID, b.SNI = loopback, offer.FrontID, offer.Names
			result.front = &frontChange{join: offer}
		}
	}
	if result.front == nil {
		if err := s.checkTunnelPort(ctx, n.PanelID, spec.Port, portOwner{}); err != nil {
			return result, err
		}
	}
	result.binding = b
	return result, nil
}

func (s *Service) applySetup(ctx context.Context, n *domain.Node, work *setupWork) (err error) {
	lines := n.Relays
	n.Relays = nil
	var created []setupListener
	var links []SetupBinding
	var forwards []setupForward
	defer func() {
		if err == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		for _, f := range forwards {
			err = errors.Join(err, s.frontSvc.UndoSetupForward(cleanup, f.frontID, n.ID, lines[f.index].ID))
		}
		for i := len(created) - 1; i >= 0; i-- {
			child := created[i].node
			confirmed := front.WithConfirmation(cleanup, &front.Confirmation{Confirmed: true})
			change, leaveErr := s.checkRemovalAgainstFronts(confirmed, child)
			if leaveErr == nil {
				leaveErr = s.finishFront(confirmed, child, change)
			}
			if leaveErr != nil {
				err = errors.Join(err, fmt.Errorf("rollback front of node %d: %w", child.ID, leaveErr))
			}
			client, removeErr := s.pool.Get(child.PanelID)
			if removeErr == nil {
				removeErr = client.DelInbound(cleanup, child.InboundID)
			}
			if removeErr != nil {
				err = errors.Join(err, fmt.Errorf("rollback inbound %d: %w", child.InboundID, removeErr))
			}
			err = errors.Join(err, s.nodes.Delete(cleanup, child.ID))
			s.inboundsChanged(cleanup, child.PanelID)
		}
		for _, link := range links {
			s.dropLinkIfUnused(cleanup, link.PeerID, link.PanelID)
		}
	}()

	for i, l := range work.listeners {
		if i > 0 {
			line := &lines[l.index]
			l.node.ParentNodeID, l.node.ParentLineID = n.ID, line.ID
			l.node.TrafficFactorMilli = line.TrafficFactorMilli
		}
		if err := s.persistInbound(ctx, l.node, l.spec, false, false); err != nil {
			return err
		}
		created = append(created, l)
		confirmed := front.WithConfirmation(ctx, &front.Confirmation{Confirmed: true, LoopbackPort: l.binding.Loopback})
		if err := s.finishFront(confirmed, l.node, l.front); err != nil {
			return err
		}
		if i > 0 {
			lines[l.index].TunnelNodeID, lines[l.index].Port = l.node.ID, 0
		}
	}
	for _, link := range work.links {
		actual, err := s.tunnelLinks.EnsureLink(ctx, link.PeerID, link.PanelID)
		if err != nil {
			return err
		}
		if !link.Reused {
			links = append(links, link)
		}
		if actual.Port != link.Port {
			return fmt.Errorf("%w: a transit port changed; review the setup again", domain.ErrConflict)
		}
	}
	n.Relays = lines
	if err := s.nodes.UpdateMetadata(ctx, n); err != nil {
		return err
	}
	for _, f := range work.forwards {
		id, err := s.frontSvc.SetupForward(ctx, n, lines[f.index], false)
		if err != nil {
			return err
		}
		if id != 0 {
			forwards = append(forwards, setupForward{frontID: id, index: f.index})
		}
		if id != f.frontID {
			return fmt.Errorf("%w: a shared port changed; review the setup again", domain.ErrConflict)
		}
	}
	if len(work.routes) > 0 {
		if err := s.setupRouter.StoreSetup(ctx, n.ID, work.routes); err != nil {
			return err
		}
		n.DestRoutes = work.routes
	}
	for _, child := range created[1:] {
		if work.listeners[0].spec.Enable && lines[child.index].Enabled {
			if err := s.nodes.UpdateEnabled(ctx, child.node.ID, true); err != nil {
				return err
			}
		}
	}
	n.Enabled = work.listeners[0].spec.Enable
	if n.Enabled {
		if err := s.nodes.UpdateEnabled(ctx, n.ID, true); err != nil {
			return err
		}
	}
	for _, l := range created {
		s.inboundsChanged(ctx, l.node.PanelID)
	}
	s.syncExistingUsersToNodeInBackground(n)
	return nil
}
