package vlessroute

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/google/uuid"
)

type Service struct {
	repos      ports.Repos
	pool       ports.XUIPool
	invalidate func()
}

func New(repos ports.Repos, pool ports.XUIPool, invalidate func()) *Service {
	return &Service{repos: repos, pool: pool, invalidate: invalidate}
}

type LineInput struct {
	ID           string   `json:"id,omitempty"`
	Name         string   `json:"name"`
	OnlyLineName bool     `json:"only_line_name"`
	EntryRelayID string   `json:"entry_relay_id,omitempty"`
	OutboundTag  string   `json:"outbound_tag"`
	Network      string   `json:"network,omitempty"`
	Protocols    []string `json:"protocols,omitempty"`
	IP           []string `json:"ip,omitempty"`
	Domain       []string `json:"domain,omitempty"`
	Port         string   `json:"port,omitempty"`
	Enabled      bool     `json:"enabled"`
}
type SaveRequest struct {
	Revision int64       `json:"revision"`
	Lines    []LineInput `json:"lines"`
}
type View struct {
	NodeID    int64                 `json:"node_id"`
	Supported bool                  `json:"supported"`
	Reason    string                `json:"reason,omitempty"`
	State     domain.NodeRouteState `json:"state"`
	Warnings  []string              `json:"warnings"`
}

func (s *Service) Get(ctx context.Context, id int64) (*View, error) {
	n, err := s.repos.Node.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	p, err := s.repos.XUIPanel.GetByID(ctx, n.PanelID)
	if err != nil {
		return nil, err
	}
	view := viewFor(n)
	view.Supported = s.repos.NodeRoute != nil && strings.EqualFold(n.DesiredProtocol, "vless") && domain.NormalizePanelKind(p.Kind) == domain.PanelKind3XUI
	if !view.Supported {
		view.Reason = "requires_3xui_vless"
	}
	if view.State.Status == "applied" && (!view.Supported || view.State.Binding != domain.RouteBinding(p.URL, n)) {
		view.State.Status = "drift"
		view.State.Error = "unsupported_or_changed_binding"
	}
	return view, nil
}

func viewFor(n *domain.Node) *View {
	state := n.RouteState.Visible()
	return &View{NodeID: n.ID, State: state, Warnings: []string{}}
}

func (s *Service) resources(ctx context.Context, id int64, cleanupOnly ...bool) (*domain.Node, *domain.XUIPanel, ports.PanelClient, ports.VlessRouteAdapter, error) {
	n, err := s.repos.Node.GetByID(ctx, id)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	p, err := s.repos.XUIPanel.GetByID(ctx, n.PanelID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if s.repos.NodeRoute == nil || domain.NormalizePanelKind(p.Kind) != domain.PanelKind3XUI || (!strings.EqualFold(n.DesiredProtocol, "vless") && !(len(cleanupOnly) > 0 && cleanupOnly[0])) {
		return nil, nil, nil, nil, fmt.Errorf("%w: routing lines are unavailable for this node", domain.ErrValidation)
	}
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	a, ok := c.(ports.VlessRouteAdapter)
	if !ok {
		return nil, nil, nil, nil, ports.ErrPanelCapabilityUnsupported
	}
	status, err := c.GetServerStatus(ctx)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: cannot verify core status", domain.ErrUnavailable)
	}
	if status == nil || status.XrayState != "running" {
		return nil, nil, nil, nil, fmt.Errorf("%w: requires a running Xray core", domain.ErrValidation)
	}
	return n, p, c, a, nil
}

func (s *Service) Outbounds(ctx context.Context, id int64) ([]ports.RouteOutbound, error) {
	_, _, _, a, err := s.resources(ctx, id)
	if err != nil {
		return nil, err
	}
	template, err := a.ReadRouteTemplate(ctx)
	if err != nil {
		return nil, err
	}
	return template.Outbounds, nil
}

func (s *Service) store(ctx context.Context, n *domain.Node, state *domain.NodeRouteState) error {
	if err := s.repos.NodeRoute.SaveRouteState(ctx, n.ID, n.RouteState.Revision, state); err != nil {
		return err
	}
	n.RouteState = *state
	if s.invalidate != nil {
		s.invalidate()
	}
	return nil
}

func (s *Service) Save(ctx context.Context, id int64, req SaveRequest) (*View, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	_, p, c, a, err := s.resources(ctx, id)
	if err != nil {
		return nil, err
	}
	backend := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimRight(p.URL, "/"))))
	owner := uuid.NewString()
	acquired, err := s.repos.NodeRoute.AcquireRouteLease(ctx, backend, owner, time.Now().UTC().Add(90*time.Second))
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, fmt.Errorf("%w: another routing update is running", domain.ErrConflict)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.repos.NodeRoute.ReleaseRouteLease(releaseCtx, backend, owner)
	}()
	// Re-read after acquiring the backend lease, not the pre-lease snapshot.
	n, err := s.repos.Node.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	currentPanel, err := s.repos.XUIPanel.GetByID(ctx, n.PanelID)
	if err != nil {
		return nil, err
	}
	if n.PanelID != p.ID || strings.TrimRight(currentPanel.URL, "/") != strings.TrimRight(p.URL, "/") {
		return nil, fmt.Errorf("%w: node backend changed; reload and retry", domain.ErrConflict)
	}
	if req.Revision != n.RouteState.Revision {
		return nil, fmt.Errorf("%w: reload node lines before saving", domain.ErrConflict)
	}
	inbound, err := c.GetInbound(ctx, n.InboundID)
	if err != nil {
		return nil, err
	}
	if inbound.Tag == "" || inbound.Protocol != "vless" || inbound.Port != n.DesiredPort {
		return nil, fmt.Errorf("%w: inbound endpoint is not in sync", domain.ErrConflict)
	}
	template, err := a.ReadRouteTemplate(ctx)
	if err != nil {
		return nil, err
	}
	used := map[uint16]bool{0: true}
	var settings struct {
		Clients []struct {
			ID string `json:"id"`
		} `json:"clients"`
	}
	if json.Unmarshal([]byte(inbound.Settings), &settings) != nil {
		return nil, fmt.Errorf("%w: cannot read inbound credentials", domain.ErrUnavailable)
	}
	for _, client := range settings.Clients {
		route, err := domain.UUIDRoute(client.ID)
		if err != nil {
			return nil, err
		}
		used[route] = true
	}
	credentialRoutes := map[uint16]bool{}
	for route := range used {
		credentialRoutes[route] = true
	}
	if err := reserveExistingRoutes(template, n.ID, inbound.Tag, n.RouteState.Lines, used); err != nil {
		return nil, err
	}
	target, err := makeTarget(n.RouteState, req.Lines, used, credentialRoutes)
	if err != nil {
		return nil, err
	}
	for _, line := range target.Lines {
		if line.Deleted || !line.Enabled || line.EntryRelayID == "" {
			continue
		}
		found := false
		for _, relay := range n.Relays {
			found = found || relay.ID == line.EntryRelayID
		}
		if !found {
			return nil, fmt.Errorf("%w: selected relay entry no longer exists; reload node lines", domain.ErrValidation)
		}
	}
	peers, err := s.peerRuleTags(ctx, n.ID, p.URL)
	if err != nil {
		return nil, err
	}
	compiled, warnings, err := Compile(template, n.ID, inbound.Tag, n.RouteState.Lines, target.Lines, peers...)
	if err != nil {
		return nil, err
	}
	// Whole-template upstream API has no CAS. Detect an intervening edit,
	// then require a single external writer throughout this application window.
	latest, err := a.ReadRouteTemplate(ctx)
	if err != nil {
		return nil, err
	}
	if !sameConfig(template.Config, latest.Config) || template.TestURL != latest.TestURL {
		return nil, fmt.Errorf("%w: 3X-UI template changed; reload and retry", domain.ErrConflict)
	}
	target.Status = "applying"
	target.Error = ""
	target.Binding = domain.RouteBinding(p.URL, n)
	if err := s.store(ctx, n, &target); err != nil {
		return nil, err
	}
	if err := a.WriteRouteTemplate(ctx, compiled); err != nil {
		return s.unknown(n, "remote_update_unconfirmed")
	}
	verified, err := a.ReadRouteTemplate(ctx)
	if err != nil || !sameConfig(compiled.Config, verified.Config) {
		return s.unknown(n, "remote_readback_mismatch")
	}
	status, err := c.GetServerStatus(ctx)
	if err != nil || status == nil || status.XrayState != "running" {
		return s.unknown(n, "core_running_not_confirmed")
	}
	target.Status = "applied"
	if err := s.store(ctx, n, &target); err != nil {
		return nil, fmt.Errorf("%w: remote updated but local confirmation failed; reload before retry", domain.ErrUnavailable)
	}
	view := viewFor(n)
	view.Supported = true
	view.Warnings = warnings
	return view, nil
}

func (s *Service) unknown(n *domain.Node, reason string) (*View, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	state := n.RouteState
	state.Status = "unknown"
	state.Error = reason
	if err := s.store(ctx, n, &state); err != nil {
		return nil, fmt.Errorf("%w: remote result unknown; reload before retry", domain.ErrUnavailable)
	}
	view := viewFor(n)
	view.Supported = true
	return view, nil
}

func makeTarget(previous domain.NodeRouteState, input []LineInput, used map[uint16]bool, originalRoutes ...map[uint16]bool) (domain.NodeRouteState, error) {
	invalid := func(message string) (domain.NodeRouteState, error) {
		return domain.NodeRouteState{}, fmt.Errorf("%w: %s", domain.ErrValidation, message)
	}
	if len(input) > 64 {
		return invalid("at most 64 routing lines are allowed")
	}
	old := map[string]domain.VlessRouteLine{}
	credentialRoutes := map[uint16]bool{}
	for route := range used {
		credentialRoutes[route] = true
	}
	if len(originalRoutes) > 0 {
		credentialRoutes = originalRoutes[0]
	}
	for _, line := range previous.Lines {
		old[line.ID] = line
		used[line.Route] = true
	}
	seen := map[string]bool{}
	state := domain.NodeRouteState{Revision: previous.Revision, Lines: []domain.VlessRouteLine{}}
	for _, request := range input {
		line := domain.VlessRouteLine{ID: request.ID, Name: strings.TrimSpace(request.Name), OnlyLineName: request.OnlyLineName, EntryRelayID: strings.TrimSpace(request.EntryRelayID), OutboundTag: strings.TrimSpace(request.OutboundTag), Enabled: request.Enabled}
		if len(line.Name) > 128 || line.OutboundTag == "" || len(line.OutboundTag) > 255 || len(line.EntryRelayID) > 128 {
			return invalid("outbound tag is required and line fields must be bounded")
		}
		line.Network, line.Protocols, line.IP, line.Domain, line.Port = request.Network, request.Protocols, request.IP, request.Domain, request.Port
		line, err := normalizeRouteMatch(line)
		if err != nil {
			return domain.NodeRouteState{}, err
		}
		if line.ID == "" {
			line.ID = uuid.NewString()
			// Reserve the UUIDv4 route range for normal freshly generated
			// credentials, preventing future PSP rotations from colliding.
			for candidate := 1; candidate <= 65535; candidate++ {
				r := uint16(candidate)
				if r >= 0x4000 && r <= 0x4fff {
					continue
				}
				if !used[r] {
					line.Route = r
					used[r] = true
					break
				}
			}
			if line.Route == 0 {
				return invalid("no free route value")
			}
		} else {
			prior, ok := old[line.ID]
			if !ok || prior.Deleted {
				return invalid("unknown or deleted routing line")
			}
			line.Route = prior.Route
			if credentialRoutes[line.Route] && line.Enabled {
				return invalid("route value conflicts with an original inbound credential")
			}
		}
		if seen[line.ID] {
			return invalid("duplicate routing line ID")
		}
		seen[line.ID] = true
		state.Lines = append(state.Lines, line)
	}
	for _, line := range previous.Lines {
		if !seen[line.ID] {
			line.Deleted = true
			line.Enabled = false
			state.Lines = append(state.Lines, line)
		}
	}
	return state, nil
}

func (s *Service) peerRuleTags(ctx context.Context, nodeID int64, backendURL string) ([]string, error) {
	panels, err := s.repos.XUIPanel.List(ctx)
	if err != nil {
		return nil, err
	}
	same := map[int64]bool{}
	for _, panel := range panels {
		if strings.TrimRight(panel.URL, "/") == strings.TrimRight(backendURL, "/") {
			same[panel.ID] = true
		}
	}
	nodes, err := s.repos.Node.List(ctx)
	if err != nil {
		return nil, err
	}
	tags := []string{}
	for _, node := range nodes {
		if node.ID != nodeID && same[node.PanelID] {
			for _, line := range node.RouteState.Lines {
				tags = append(tags, RuleTag(node.ID, line.ID))
			}
		}
	}
	return tags, nil
}

func sameConfig(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, error) {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	left, lerr := decode(a)
	right, rerr := decode(b)
	return lerr == nil && rerr == nil && reflect.DeepEqual(left, right)
}
