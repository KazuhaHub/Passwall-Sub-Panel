package vlessroute

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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
		if _, err := s.checkGroup(ctx, p, group, false); err != nil {
			return err
		}
	}
	return nil
}

// EditorView combines a verified node view and its read-only outbound catalog.
type EditorView struct {
	*View
	Outbounds      []ports.RouteOutbound `json:"outbounds"`
	OutboundsError string                `json:"outbounds_error,omitempty"`
}

func (s *Service) Refresh(ctx context.Context, id int64) (*View, error) {
	result, err := s.refresh(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return result.View, nil
}

func (s *Service) RefreshEditor(ctx context.Context, id int64) (*EditorView, error) {
	return s.refresh(ctx, id, true)
}

func (s *Service) refresh(ctx context.Context, id int64, catalog bool) (*EditorView, error) {
	n, err := s.repos.Node.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	p, err := s.repos.XUIPanel.GetByID(ctx, n.PanelID)
	if err != nil {
		return nil, err
	}
	result := &EditorView{View: s.nodeView(n, p), Outbounds: []ports.RouteOutbound{}}
	if !result.Supported || len(n.RouteState.Lines) == 0 && !catalog {
		return result, nil
	}
	checked, err := s.checkGroup(ctx, p, []*domain.Node{n}, true)
	if err != nil {
		return nil, err
	}
	n = checked.nodes[id]
	if n.PanelID != p.ID {
		p, err = s.repos.XUIPanel.GetByID(ctx, n.PanelID)
		if err != nil {
			return nil, err
		}
		checked.catalogAvailable = false
	} else {
		p = checked.panel
	}
	result.View = s.nodeView(n, p)
	if catalog && result.Supported {
		if checked.catalogAvailable {
			if checked.template.Outbounds != nil {
				result.Outbounds = checked.template.Outbounds
			}
		} else {
			result.OutboundsError = "catalog_unavailable"
		}
	}
	return result, nil
}

type routeCheckResult struct {
	nodes            map[int64]*domain.Node
	panel            *domain.XUIPanel
	template         ports.RouteTemplate
	catalogAvailable bool
}

// The unlocked scan only chooses work. Each lease owns one remote snapshot and
// one fresh registry; credentials from different panel records are not merged.
func (s *Service) checkGroup(parent context.Context, p *domain.XUIPanel, nodes []*domain.Node, failIfBusy bool) (*routeCheckResult, error) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	release, acquired, err := s.acquireBackendLease(ctx, p.URL, failIfBusy)
	if err != nil || !acquired {
		return nil, err
	}
	defer release()
	c, readErr := s.pool.Get(p.ID)
	adapter, ok := c.(ports.VlessRouteAdapter)
	if readErr == nil && !ok {
		readErr = ports.ErrPanelCapabilityUnsupported
	}
	var status *ports.ServerStatus
	result := &routeCheckResult{nodes: map[int64]*domain.Node{}}
	if readErr == nil {
		status, readErr = c.GetServerStatus(ctx)
	}
	if readErr == nil {
		result.template, readErr = adapter.ReadRouteTemplate(ctx)
		result.catalogAvailable = readErr == nil && status != nil && status.XrayState == "running"
	}
	registry, err := s.backendNodes(ctx, p.URL)
	if err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	for _, n := range registry {
		result.nodes[n.ID] = n
		for _, line := range n.RouteState.Lines {
			owned[RuleTag(n.ID, line.ID)] = true
		}
	}
	needsInbounds := false
	for _, snapshot := range nodes {
		n := result.nodes[snapshot.ID]
		if n == nil {
			// Metadata edits can move nodes without taking the routing lease.
			n, err = s.repos.Node.GetByID(ctx, snapshot.ID)
			if err != nil {
				return nil, err
			}
			result.nodes[n.ID] = n
		}
		needsInbounds = needsInbounds || len(n.RouteState.Lines) > 0
	}
	var inbounds []ports.Inbound
	if readErr == nil && needsInbounds {
		inbounds, readErr = c.ListInbounds(ctx)
	}
	actual := map[int]*ports.Inbound{}
	for i := range inbounds {
		actual[inbounds[i].ID] = &inbounds[i]
	}
	// Panel metadata can change during a slow remote read. Keep this protected
	// re-read rather than confirming an old backend snapshot under a new URL.
	result.panel, err = s.repos.XUIPanel.GetByID(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	backendUnchanged := strings.TrimRight(result.panel.URL, "/") == strings.TrimRight(p.URL, "/") &&
		domain.NormalizePanelKind(result.panel.Kind) == domain.PanelKind3XUI
	result.catalogAvailable = result.catalogAvailable && backendUnchanged
	parsed, _ := parseRouteTemplate(result.template)
	tailValid := true
	if parsed != nil {
		tail := false
		for _, rule := range parsed.rules {
			if owned[rule.tag] {
				tail = true
			} else if tail {
				tailValid = false
			}
		}
	}
	for _, snapshot := range nodes {
		// Ordinary node metadata edits do not take the routing lease. Re-read
		// after remote I/O so an old inbound cannot confirm a changed binding.
		n, err := s.repos.Node.GetByID(ctx, snapshot.ID)
		if err != nil {
			return nil, err
		}
		result.nodes[n.ID] = n
		if len(n.RouteState.Lines) == 0 {
			continue
		}
		next := n.RouteState
		next.Status, next.Error = "applied", ""
		if readErr != nil {
			next.Status, next.Error = "unknown", "remote_check_unavailable"
		} else if status == nil || domain.NormalizePanelKind(p.Kind) != domain.PanelKind3XUI || status.XrayState != "running" {
			next.Status, next.Error = "drift", "unsupported_or_stopped_core"
		} else if inbound := actual[n.InboundID]; !backendUnchanged || n.PanelID != p.ID || inbound == nil || inbound.Protocol != "vless" || inbound.Port != n.DesiredPort || !domain.RouteBackendMatches(n, result.panel) {
			next.Status, next.Error = "drift", "inbound_binding_changed"
		} else if !tailValid || !matchesNodeRules(parsed, n, inbound) {
			next.Status, next.Error = "drift", "routing_or_outbound_changed"
		}
		if next.Status != n.RouteState.Status || next.Error != n.RouteState.Error {
			if err := s.store(ctx, n, &next); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func matchesNodeRules(snapshot *routeTemplateSnapshot, n *domain.Node, inbound *ports.Inbound) bool {
	if snapshot == nil {
		return false
	}
	expected, err := compileDesiredRules(n.ID, inbound.Tag, n.RouteState.Lines, snapshot.outbounds)
	if err != nil {
		return false
	}
	positions := []int{}
	for _, line := range n.RouteState.Lines {
		positions = append(positions, snapshot.rulePositions[RuleTag(n.ID, line.ID)]...)
	}
	if len(positions) != len(expected) {
		return false
	}
	sort.Ints(positions)
	for index, position := range positions {
		if !sameConfig(snapshot.rules[position].raw, expected[index]) {
			return false
		}
	}
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
	release, _, err := s.acquireBackendLease(ctx, p.URL, true)
	if err != nil {
		return err
	}
	defer release()
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
