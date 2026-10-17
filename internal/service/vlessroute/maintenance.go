package vlessroute

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/google/uuid"
)

// CheckAll performs only remote reads. A mismatch withdraws the affected
// local entries; it never repairs or reorders an administrator's template.
func (s *Service) CheckAll(ctx context.Context) error {
	if s.repos.NodeRoute == nil {
		return nil
	}
	nodes, err := s.repos.Node.List(ctx)
	if err != nil {
		return err
	}
	byPanel := map[int64][]*domain.Node{}
	for _, n := range nodes {
		if len(n.RouteState.Lines) > 0 {
			byPanel[n.PanelID] = append(byPanel[n.PanelID], n)
		}
	}
	for panelID, group := range byPanel {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p, err := s.repos.XUIPanel.GetByID(ctx, panelID)
		if err != nil {
			return err
		}
		if err := s.checkGroup(ctx, p, group, false); err != nil {
			return err
		}
	}
	return nil
}

// Refresh verifies one node against the remote template before returning its
// latest local view. It never applies or repairs remote routing rules.
func (s *Service) Refresh(ctx context.Context, id int64) (*View, error) {
	view, err := s.Get(ctx, id)
	if err != nil || !view.Supported {
		return view, err
	}
	n, err := s.repos.Node.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(n.RouteState.Lines) == 0 {
		return view, nil
	}
	p, err := s.repos.XUIPanel.GetByID(ctx, n.PanelID)
	if err != nil {
		return nil, err
	}
	if err := s.checkGroup(ctx, p, []*domain.Node{n}, true); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) checkGroup(parent context.Context, p *domain.XUIPanel, nodes []*domain.Node, failIfBusy bool) error {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	backend := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimRight(p.URL, "/"))))
	owner := uuid.NewString()
	acquired, err := s.repos.NodeRoute.AcquireRouteLease(ctx, backend, owner, time.Now().UTC().Add(90*time.Second))
	if err != nil {
		return err
	}
	if !acquired {
		if failIfBusy {
			return fmt.Errorf("%w: another routing operation is running; retry refresh", domain.ErrConflict)
		}
		return nil
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.repos.NodeRoute.ReleaseRouteLease(releaseCtx, backend, owner)
	}()
	c, poolErr := s.pool.Get(p.ID)
	adapter, ok := c.(ports.VlessRouteAdapter)
	var template ports.RouteTemplate
	var inbounds []ports.Inbound
	var status *ports.ServerStatus
	readErr := poolErr
	if readErr == nil && !ok {
		readErr = ports.ErrPanelCapabilityUnsupported
	}
	if readErr == nil {
		status, readErr = c.GetServerStatus(ctx)
	}
	if readErr == nil {
		template, readErr = adapter.ReadRouteTemplate(ctx)
	}
	if readErr == nil {
		inbounds, readErr = c.ListInbounds(ctx)
	}
	actual := map[int]*ports.Inbound{}
	for i := range inbounds {
		actual[inbounds[i].ID] = &inbounds[i]
	}
	owned := map[string]bool{}
	// Registered peers on the same backend also form part of the tail block.
	peerTags, err := s.peerRuleTags(ctx, 0, p.URL)
	if err != nil {
		return err
	}
	for _, tag := range peerTags {
		owned[tag] = true
	}
	for _, snapshot := range nodes {
		n, err := s.repos.Node.GetByID(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		next := n.RouteState
		next.Status = "applied"
		next.Error = ""
		if readErr != nil {
			next.Status = "unknown"
			next.Error = "remote_check_unavailable"
		} else if status == nil || domain.NormalizePanelKind(p.Kind) != domain.PanelKind3XUI || status.XrayState != "running" {
			next.Status = "drift"
			next.Error = "unsupported_or_stopped_core"
		} else if inbound := actual[n.InboundID]; n.PanelID != p.ID || inbound == nil || inbound.Protocol != "vless" || inbound.Port != n.DesiredPort || n.RouteState.Binding != domain.RouteBinding(p.URL, n) {
			next.Status = "drift"
			next.Error = "inbound_binding_changed"
		} else if !matchesNodeRules(template, n, inbound, owned) {
			next.Status = "drift"
			next.Error = "routing_or_outbound_changed"
		}
		if next.Status == n.RouteState.Status && next.Error == n.RouteState.Error {
			continue
		}
		if err := s.store(ctx, n, &next); err != nil {
			return err
		}
	}
	return nil
}

func matchesNodeRules(template ports.RouteTemplate, n *domain.Node, inbound *ports.Inbound, allOwned map[string]bool) bool {
	compiled, _, err := Compile(template, n.ID, inbound.Tag, n.RouteState.Lines, n.RouteState.Lines)
	if err != nil {
		return false
	}
	readRules := func(config json.RawMessage) ([]json.RawMessage, bool) {
		var root struct {
			Routing struct {
				Rules []json.RawMessage `json:"rules"`
			} `json:"routing"`
		}
		err := json.Unmarshal(config, &root)
		return root.Routing.Rules, err == nil
	}
	actual, ok := readRules(template.Config)
	if !ok {
		return false
	}
	expected, ok := readRules(compiled.Config)
	if !ok {
		return false
	}
	mine := map[string]bool{}
	for _, line := range n.RouteState.Lines {
		mine[RuleTag(n.ID, line.ID)] = true
	}
	selectMine := func(rules []json.RawMessage) ([]json.RawMessage, bool) {
		result := []json.RawMessage{}
		tail := false
		for _, raw := range rules {
			var rule struct {
				Tag string `json:"ruleTag"`
			}
			if json.Unmarshal(raw, &rule) != nil {
				return nil, false
			}
			if allOwned[rule.Tag] {
				tail = true
			} else if tail {
				return nil, false
			}
			if mine[rule.Tag] {
				result = append(result, raw)
			}
		}
		return result, true
	}
	left, leftOK := selectMine(actual)
	right, rightOK := selectMine(expected)
	if !leftOK || !rightOK || len(left) != len(right) {
		return false
	}
	for i := range left {
		if !sameConfig(left[i], right[i]) {
			return false
		}
	}
	// An original client UUID must not already carry an allocated route value.
	var settings struct {
		Clients []struct {
			ID string `json:"id"`
		} `json:"clients"`
	}
	if json.Unmarshal([]byte(inbound.Settings), &settings) != nil {
		return false
	}
	for _, client := range settings.Clients {
		route, err := domain.UUIDRoute(client.ID)
		if err != nil {
			return false
		}
		for _, line := range n.RouteState.Lines {
			if line.Enabled && !line.Deleted && line.Route == route {
				return false
			}
		}
	}
	return true
}

// RemoveForNode withdraws only this node's registered rules. Configuration is
// kept until the node is deleted, so any failure remains retryable. No inbound
// read is required: this also cleans up an already removed remote listener.
func (s *Service) RemoveForNode(parent context.Context, id int64) error {
	n, err := s.repos.Node.GetByID(parent, id)
	if err != nil {
		return err
	}
	if len(n.RouteState.Lines) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	_, p, _, adapter, err := s.resources(ctx, id, true)
	if err != nil {
		return err
	}
	backend := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimRight(p.URL, "/"))))
	owner := uuid.NewString()
	acquired, err := s.repos.NodeRoute.AcquireRouteLease(ctx, backend, owner, time.Now().UTC().Add(90*time.Second))
	if err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("%w: another routing update is running", domain.ErrConflict)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.repos.NodeRoute.ReleaseRouteLease(releaseCtx, backend, owner)
	}()
	n, err = s.repos.Node.GetByID(ctx, id)
	if err != nil {
		return err
	}
	currentPanel, err := s.repos.XUIPanel.GetByID(ctx, n.PanelID)
	if err != nil {
		return err
	}
	if n.PanelID != p.ID || strings.TrimRight(currentPanel.URL, "/") != strings.TrimRight(p.URL, "/") {
		return fmt.Errorf("%w: node backend changed during cleanup", domain.ErrConflict)
	}
	template, err := adapter.ReadRouteTemplate(ctx)
	if err != nil {
		return err
	}
	compiled, _, err := Compile(template, id, "cleanup-only", n.RouteState.Lines, nil)
	if err != nil {
		return err
	}
	state := n.RouteState
	state.Status = "drift"
	state.Error = "routes_withdrawn"
	if err := s.store(ctx, n, &state); err != nil {
		return err
	}
	latest, err := adapter.ReadRouteTemplate(ctx)
	if err != nil {
		return err
	}
	if !sameConfig(latest.Config, template.Config) || latest.TestURL != template.TestURL {
		return fmt.Errorf("%w: backend template changed during cleanup", domain.ErrConflict)
	}
	if err := adapter.WriteRouteTemplate(ctx, compiled); err != nil {
		return fmt.Errorf("%w: route cleanup result unknown; node retained", domain.ErrUnavailable)
	}
	verified, err := adapter.ReadRouteTemplate(ctx)
	if err != nil || !sameConfig(compiled.Config, verified.Config) {
		return fmt.Errorf("%w: route cleanup not confirmed; node retained", domain.ErrUnavailable)
	}
	return nil
}
