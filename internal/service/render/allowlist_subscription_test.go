package render

import (
	"context"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
)

type allowlistGroupRepo struct {
	ports.GroupRepo
	g *domain.Group
}

func (r allowlistGroupRepo) GetByID(context.Context, int64) (*domain.Group, error) { return r.g, nil }

type enabledNodes struct {
	ports.NodeRepo
	nodes []*domain.Node
}

func (r enabledNodes) ListEnabled(context.Context) ([]*domain.Node, error) { return r.nodes, nil }

type kindPanels struct{ kinds map[int64]domain.PanelKind }

func (r kindPanels) GetByID(_ context.Context, id int64) (*domain.XUIPanel, error) {
	return &domain.XUIPanel{ID: id, Kind: r.kinds[id]}, nil
}

type fixedAgents struct{ agent *domain.NodeAgent }

func (r fixedAgents) GetByPanelID(_ context.Context, id int64) (*domain.NodeAgent, error) {
	if id != r.agent.PanelID {
		return nil, domain.ErrNotFound
	}
	return r.agent, nil
}

type noPolicyState struct{}

func (noPolicyState) GetMeta(context.Context, string) (*domain.DestAgentPolicy, error) {
	return nil, domain.ErrNotFound
}

// A member of an allowlist group must not be handed a node that cannot enforce
// the allowlist: /sub resolves the member's nodes through the group service, so
// the exclusion has to hold in the rendered subscription, not only in the
// client sync. And with the allowlist off, the subscription is what it was.
func TestSubscriptionOfAnAllowlistMemberExcludesNodesThatCannotEnforce(t *testing.T) {
	for _, tc := range []struct {
		state      domain.DestState
		wantNative bool
		want3XUI   bool
	}{
		{domain.DestStateOff, true, true},
		{"", true, true},
		{domain.DestStateReport, true, false},
		{domain.DestStateOn, true, false},
	} {
		t.Run("state="+string(tc.state), func(t *testing.T) {
			s, native, u, _ := referenceRenderFixture(t)
			native.DisplayName = "native-node"
			native.RouteState = domain.NodeRouteState{}
			legacy := *native
			legacy.ID, legacy.PanelID, legacy.InboundID, legacy.DisplayName = 8, 2, 4, "legacy-3xui-node"

			g := &domain.Group{ID: 1, TagFilter: domain.TagFilter{All: true}, DestAllowlist: domain.DestAllowlist{State: tc.state}}
			svc := group.New(allowlistGroupRepo{g: g}, enabledNodes{nodes: []*domain.Node{native, &legacy}}, nil)
			svc.SetEligibilityDeps(group.EligibilityDeps{
				Panels: kindPanels{kinds: map[int64]domain.PanelKind{1: domain.PanelKindPSP, 2: domain.PanelKind3XUI}},
				Agents: fixedAgents{agent: &domain.NodeAgent{AgentID: "agt_1", PanelID: 1, DesiredCoreEngine: domain.NodeCoreXray,
					ObservedCapabilities: []string{protocol.CapabilityDestinationPolicy, protocol.CapabilityDestinationGroups}}},
				Policies: noPolicyState{},
			})
			s.repos.Group = allowlistGroupRepo{g: g}
			s.groupSvc = svc
			s.repos.PSPClient = renderCredentialRepo{
				client: &domain.PSPClient{ID: 11, UserID: u.ID, UUID: u.UUID},
				attachments: []domain.PSPClientInbound{
					{ClientID: 11, NodeID: native.ID, AppliedUUID: "12345678-1234-4abc-8def-123456789abc", AppliedEmail: "u5@example.test"},
					{ClientID: 11, NodeID: legacy.ID, AppliedUUID: "12345678-1234-4abc-8def-123456789abd", AppliedEmail: "u5@example.test"},
				},
			}
			s.repos.RuleSet = referenceRuleRepo{rule: &domain.RuleSet{Slug: "test", Enabled: true, Content: "- MATCH,国外媒体",
				ProxyGroupMembers: map[string][]domain.ProxyGroupMember{"国外媒体": {{Kind: "node", NodeID: native.ID}, {Kind: "node", NodeID: legacy.ID}}}}}
			s.cache = newRenderCache(subRenderCacheTTL)

			out, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			if got := strings.Contains(body, "native-node"); got != tc.wantNative {
				t.Errorf("native node in subscription = %v, want %v\n%s", got, tc.wantNative, body)
			}
			if got := strings.Contains(body, "legacy-3xui-node"); got != tc.want3XUI {
				t.Errorf("3X-UI node in subscription = %v, want %v\n%s", got, tc.want3XUI, body)
			}
		})
	}
}
