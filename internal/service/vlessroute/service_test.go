package vlessroute

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type testNodeRepo struct {
	ports.NodeRepo
	node             domain.Node
	failConfirmation bool
	leaseBusy        bool
}

func (r *testNodeRepo) GetByID(context.Context, int64) (*domain.Node, error) {
	n := r.node
	n.RouteState.Lines = append([]domain.VlessRouteLine(nil), n.RouteState.Lines...)
	return &n, nil
}
func (r *testNodeRepo) List(context.Context) ([]*domain.Node, error) {
	n, _ := r.GetByID(context.Background(), 1)
	return []*domain.Node{n}, nil
}
func (r *testNodeRepo) SaveRouteState(_ context.Context, _ int64, revision int64, state *domain.NodeRouteState) error {
	if revision != r.node.RouteState.Revision {
		return domain.ErrConflict
	}
	if r.failConfirmation && state.Status == "applied" {
		return errors.New("database unavailable")
	}
	state.Revision++
	r.node.RouteState = *state
	return nil
}
func (r *testNodeRepo) AcquireRouteLease(context.Context, string, string, time.Time) (bool, error) {
	return !r.leaseBusy, nil
}
func (r *testNodeRepo) ReleaseRouteLease(context.Context, string, string) error { return nil }

type testPanelRepo struct {
	ports.XUIPanelRepo
	panel domain.XUIPanel
}

func (r *testPanelRepo) GetByID(context.Context, int64) (*domain.XUIPanel, error) {
	p := r.panel
	return &p, nil
}
func (r *testPanelRepo) List(context.Context) ([]*domain.XUIPanel, error) {
	p := r.panel
	return []*domain.XUIPanel{&p}, nil
}

type testPool struct {
	ports.XUIPool
	client *testClient
}

func (p testPool) Get(int64) (ports.PanelClient, error) { return p.client, nil }

type testClient struct {
	ports.PanelClient
	template          ports.RouteTemplate
	writes, reads     int
	writeError        bool
	driftOnSecondRead bool
	coreState         string
	readError         bool
}

func (c *testClient) GetServerStatus(context.Context) (*ports.ServerStatus, error) {
	state := c.coreState
	if state == "" {
		state = "running"
	}
	return &ports.ServerStatus{XrayState: state}, nil
}
func (c *testClient) GetInbound(context.Context, int) (*ports.Inbound, error) {
	return &ports.Inbound{ID: 1, Tag: "inbound-443", Protocol: "vless", Port: 443, Settings: `{"clients":[{"id":"12345678-1234-4abc-8def-123456789abc"}]}`}, nil
}

func (c *testClient) ListInbounds(ctx context.Context) ([]ports.Inbound, error) {
	inbound, err := c.GetInbound(ctx, 1)
	if err != nil {
		return nil, err
	}
	return []ports.Inbound{*inbound}, nil
}
func (c *testClient) ReadRouteTemplate(context.Context) (ports.RouteTemplate, error) {
	c.reads++
	if c.readError {
		return ports.RouteTemplate{}, errors.New("route read unavailable")
	}
	if c.driftOnSecondRead && c.reads == 2 {
		c.template.Config = json.RawMessage(`{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"domain":["changed.example"],"outboundTag":"direct"}]}}`)
	}
	return c.template, nil
}
func (c *testClient) WriteRouteTemplate(_ context.Context, template ports.RouteTemplate) error {
	c.writes++
	c.template = template
	if c.writeError {
		return errors.New("timeout after committed write")
	}
	return nil
}

func serviceFixture(t *testing.T) (*Service, *testNodeRepo, *testClient) {
	t.Helper()
	n := &testNodeRepo{node: domain.Node{ID: 1, PanelID: 1, InboundID: 1, DesiredProtocol: "vless", DesiredPort: 443, Enabled: true}}
	p := &testPanelRepo{panel: domain.XUIPanel{ID: 1, Kind: domain.PanelKind3XUI, URL: "https://panel.example"}}
	c := &testClient{template: ports.RouteTemplate{Config: json.RawMessage(`{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"domain":["example.test"],"outboundTag":"direct"}]}}`), TestURL: "keep", Outbounds: []ports.RouteOutbound{{Tag: "direct", Protocol: "freedom"}}}}
	s := New(ports.Repos{Node: n, NodeRoute: n, XUIPanel: p}, testPool{client: c}, nil)
	return s, n, c
}

func TestSaveConfirmsAndKeepsOriginalCredentialRangeReserved(t *testing.T) {
	s, n, c := serviceFixture(t)
	view, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if view.State.Status != "applied" || view.State.Revision != 2 || c.writes != 1 || view.State.Binding != domain.RouteBinding("https://panel.example", &n.node) {
		t.Fatalf("not confirmed: %+v", view)
	}
	line := view.State.Lines[0]
	if line.Route >= 0x4000 && line.Route <= 0x4fff {
		t.Fatal("allocated a normal UUIDv4 credential's route value")
	}
	var raw map[string]any
	if err := json.Unmarshal(c.template.Config, &raw); err != nil {
		t.Fatal(err)
	}
	rules := raw["routing"].(map[string]any)["rules"].([]any)
	last := rules[len(rules)-1].(map[string]any)
	if _, restricted := last["user"]; restricted {
		t.Fatal("unexpected PSP-only user scope")
	}
	if last["ruleTag"] != RuleTag(1, line.ID) {
		t.Fatal(last)
	}
}

func TestRoutingLinesKeepBackendProtocolAndCoreStateChecks(t *testing.T) {
	for _, condition := range []string{"non_3xui", "non_vless", "stopped_core"} {
		t.Run(condition, func(t *testing.T) {
			s, n, c := serviceFixture(t)
			switch condition {
			case "non_3xui":
				s.repos.XUIPanel.(*testPanelRepo).panel.Kind = domain.PanelKindSUI
			case "non_vless":
				n.node.DesiredProtocol = "vmess"
			case "stopped_core":
				c.coreState = "stopped"
			}
			if condition != "stopped_core" {
				view, err := s.Get(context.Background(), 1)
				if err != nil || view.Supported || view.Reason != "requires_3xui_vless" {
					t.Fatalf("unsupported node was offered routing lines: %+v %v", view, err)
				}
			}
			if _, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}}); !errors.Is(err, domain.ErrValidation) || c.writes != 0 {
				t.Fatalf("invalid node reached a remote write: writes=%d err=%v", c.writes, err)
			}
		})
	}
}

func TestLineNameOnlyRoundTripDoesNotChangeRemoteRulesOrIdentity(t *testing.T) {
	s, _, c := serviceFixture(t)
	view, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true, OnlyLineName: true}}})
	if err != nil {
		t.Fatal(err)
	}
	line := view.State.Lines[0]
	read, err := s.Get(context.Background(), 1)
	if err != nil || !read.State.Lines[0].OnlyLineName {
		t.Fatalf("line-only setting lost on read: %+v %v", read, err)
	}
	before := append(json.RawMessage(nil), c.template.Config...)
	view, err = s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{ID: line.ID, Name: line.Name, OutboundTag: line.OutboundTag, Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	got := view.State.Lines[0]
	if got.OnlyLineName || got.ID != line.ID || got.Route != line.Route || !sameConfig(before, c.template.Config) {
		t.Fatal("resetting the display option changed routing or lost its default")
	}
}

func TestOptionalRouteNamesAndStableEntryReferences(t *testing.T) {
	s, n, _ := serviceFixture(t)
	n.node.HideDirect = true
	n.node.Relays = []domain.RelayLine{{ID: "front", Address: "relay.example", Enabled: true}}
	view, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{OutboundTag: "direct", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	line := view.State.Lines[0]
	if line.Name != "" || line.Label() != "direct" || line.EntryRelayID != "" {
		t.Fatal("blank name/default entry did not round-trip")
	}
	view, err = s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{ID: line.ID, OutboundTag: "direct", EntryRelayID: "front", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	read, err := s.Get(context.Background(), 1)
	if err != nil || read.State.Lines[0].EntryRelayID != "front" || read.State.Lines[0].Route != line.Route || read.State.Status != "applied" {
		t.Fatalf("entry selection lost identity or was blocked by hidden direct: %+v %v", read, err)
	}
	if _, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{ID: line.ID, OutboundTag: "direct", EntryRelayID: "missing", Enabled: true}}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("unknown entry was accepted")
	}
}

func TestAmbiguousRemoteWriteWithdrawsAndRetryDoesNotDuplicate(t *testing.T) {
	s, _, c := serviceFixture(t)
	c.writeError = true
	view, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if view.State.Status != "unknown" || view.State.Error != "remote_update_unconfirmed" {
		t.Fatalf("ambiguous write appeared applied: %+v", view)
	}
	line := view.State.Lines[0]
	firstConfig := append(json.RawMessage(nil), c.template.Config...)
	c.writeError = false
	retry, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{ID: line.ID, Name: line.Name, OutboundTag: line.OutboundTag, Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if retry.State.Status != "applied" || !sameConfig(firstConfig, c.template.Config) {
		t.Fatal("retry duplicated or retargeted the rule")
	}
}

func TestRemoteSuccessWithLocalFailureStaysUnpublished(t *testing.T) {
	s, n, _ := serviceFixture(t)
	n.failConfirmation = true
	_, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}})
	if !errors.Is(err, domain.ErrUnavailable) || n.node.RouteState.Status != "applying" {
		t.Fatalf("local failure published a route: %v %+v", err, n.node.RouteState)
	}
}

func TestInterveningExternalEditStopsBeforeWriting(t *testing.T) {
	s, n, c := serviceFixture(t)
	c.driftOnSecondRead = true
	_, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}})
	if !errors.Is(err, domain.ErrConflict) || c.writes != 0 || n.node.RouteState.Revision != 0 {
		t.Fatalf("overwrote an external edit: %v", err)
	}
}

func TestTargetRetiresWithoutReusingOrChangingLineIdentity(t *testing.T) {
	previous := domain.NodeRouteState{Lines: []domain.VlessRouteLine{{ID: "old", Name: "old", Route: 1, OutboundTag: "direct", Enabled: true}}}
	state, err := makeTarget(previous, []LineInput{{Name: "new", OutboundTag: "direct", Enabled: true}}, map[uint16]bool{0: true})
	if err != nil {
		t.Fatal(err)
	}
	if state.Lines[0].Route == 1 || !state.Lines[1].Deleted || state.Lines[1].Enabled {
		t.Fatal(state)
	}
	visible := state.Visible()
	if len(visible.Lines) != 1 {
		t.Fatal("retired line surfaced in the editor")
	}
	renamed, err := makeTarget(state, []LineInput{{ID: visible.Lines[0].ID, Name: "renamed", OutboundTag: "direct", Enabled: true}}, map[uint16]bool{0: true})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Lines[0].ID != visible.Lines[0].ID || renamed.Lines[0].Route != visible.Lines[0].Route {
		t.Fatal("rename changed identity")
	}
	if !reflect.DeepEqual(state.Lines[1], renamed.Lines[1]) {
		t.Fatal("retirement metadata lost")
	}
}

func TestMaintenanceDetectsDriftWithoutRemoteWrites(t *testing.T) {
	s, n, c := serviceFixture(t)
	_, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.node.RouteState.Status != "applied" || c.writes != 1 {
		t.Fatal("verification changed a healthy template")
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal(c.template.Config, &root)
	var routing map[string]json.RawMessage
	_ = json.Unmarshal(root["routing"], &routing)
	var rules []json.RawMessage
	_ = json.Unmarshal(routing["rules"], &rules)
	rules = append(rules, json.RawMessage(`{"network":"tcp,udp","outboundTag":"direct"}`))
	routing["rules"], _ = json.Marshal(rules)
	root["routing"], _ = json.Marshal(routing)
	c.template.Config, _ = json.Marshal(root)
	external := append(json.RawMessage(nil), c.template.Config...)
	if err := s.CheckAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.node.RouteState.Status != "drift" || c.writes != 1 || !sameConfig(external, c.template.Config) {
		t.Fatal("drift check repaired/overwrote an external template")
	}
}

func TestCleanupPreservesLineConfigUntilNodeDeletion(t *testing.T) {
	s, n, c := serviceFixture(t)
	_, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveForNode(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if n.node.RouteState.Status != "drift" || len(n.node.RouteState.Lines) != 1 {
		t.Fatal("cleanup lost its recoverable registry")
	}
	var root struct {
		Routing struct {
			Rules []json.RawMessage `json:"rules"`
		} `json:"routing"`
	}
	_ = json.Unmarshal(c.template.Config, &root)
	if len(root.Routing.Rules) != 1 {
		t.Fatal("cleanup did not preserve the custom rule")
	}
}

func TestReadViewDoesNotClaimAppliedAfterBindingChanges(t *testing.T) {
	s, n, _ := serviceFixture(t)
	n.node.RouteState = domain.NodeRouteState{Revision: 4, Status: "applied", Binding: "previous-backend", Lines: []domain.VlessRouteLine{{ID: "one", Route: 1001, Name: "B", OutboundTag: "direct", Enabled: true}}}
	view, err := s.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if view.State.Status != "drift" || view.State.Revision != 4 || n.node.RouteState.Status != "applied" {
		t.Fatal("read either lied about binding or mutated persisted state")
	}
}

func TestCleanupStillWorksAfterChangingAwayFromVless(t *testing.T) {
	s, n, _ := serviceFixture(t)
	_, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	n.node.DesiredProtocol = "vmess"
	if err := s.RemoveForNode(context.Background(), 1); err != nil {
		t.Fatalf("old routes prevented cleanup after protocol change: %v", err)
	}
	if n.node.RouteState.Status != "drift" {
		t.Fatal("cleanup left the old route publishable")
	}
}
