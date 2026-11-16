package render

import (
	"context"
	"encoding/base64"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ADR 0041: a group sees a line only when its tag_filter also holds on the
// line's own labels. Every output path consumes the same narrowed set.

type filterGroupRepo struct {
	ports.GroupRepo
	filter domain.TagFilter
}

func (r filterGroupRepo) GetByID(context.Context, int64) (*domain.Group, error) {
	return &domain.Group{ID: 1, TagFilter: r.filter}, nil
}

// Node "A" (US / edge) with relay "A R" (JP / cmcc) and route line "A B"
// (US / edge); the group asks for region:US.
func lineVisibilityFixture(t *testing.T, filter ...string) (*Service, *domain.Node, *domain.User, *domain.RuleSet) {
	t.Helper()
	s, n, u, rule := referenceRenderFixture(t)
	u.GroupID = 1
	n.Region, n.Tags = "US", []string{"edge"}
	n.Relays = []domain.RelayLine{{ID: "relay", Name: "R", Address: "relay.example", Enabled: true, Region: "JP", Tags: []string{"cmcc"}}}
	n.RouteState.Lines[0].Region, n.RouteState.Lines[0].Tags = "US", []string{"edge"}
	if len(filter) == 0 {
		filter = []string{"region:US"}
	}
	s.repos.Group = filterGroupRepo{filter: domain.TagFilter{Tags: filter}}
	return s, n, u, rule
}

func renderedNames(t *testing.T, s *Service, u *domain.User, ct domain.ClientType) map[string]bool {
	t.Helper()
	output, err := s.RenderForUser(context.Background(), u, ct)
	if err != nil {
		t.Fatal(err)
	}
	if ct == domain.ClientURIList {
		body, err := base64.StdEncoding.DecodeString(string(output.Body))
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, line := range strings.Fields(string(body)) {
			uri, err := url.Parse(line)
			if err != nil {
				t.Fatal(err)
			}
			names[uri.Fragment] = true
		}
		return names
	}
	key := "name"
	if ct == domain.ClientSingBox {
		key = "tag"
	}
	return emittedProxyNames(subscriptionProxyMaps(t, output, ct), key)
}

func TestHiddenRelayIsAbsentFromEverySubscriptionFormat(t *testing.T) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox, domain.ClientURIList} {
		t.Run(string(ct), func(t *testing.T) {
			s, _, u, rule := lineVisibilityFixture(t)
			rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "node_set", Value: "remaining"}}
			names := renderedNames(t, s, u, ct)
			if names["A R"] {
				t.Fatalf("hidden relay published: %v", names)
			}
			if !names["A"] || !names["A B"] {
				t.Fatalf("visible entries lost: %v", names)
			}
		})
	}
}

func TestEveryMemberKindSeesOnlyVisibleLines(t *testing.T) {
	for _, member := range []domain.ProxyGroupMember{
		{Kind: "node", NodeID: 7},
		{Kind: "line", NodeID: 7, LineID: "relay"},
		{Kind: "node_set", Value: "remaining"},
		{Kind: "node_set", Value: "region:JP"},
		{Kind: "node_set", Value: "tag:cmcc"},
	} {
		t.Run(member.Kind+member.Value+member.LineID, func(t *testing.T) {
			s, _, u, rule := lineVisibilityFixture(t)
			rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{member}
			preview, err := s.PreviewMihomoGroup(context.Background(), u.GroupID, rule)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range preview.Groups[0].Members {
				if name == "A R" {
					t.Fatalf("%+v selected a hidden line: %v", member, preview.Groups[0].Members)
				}
			}
		})
	}
}

// A group that sees the relay selects it by the relay's own tag. Whether a
// group passes the node at all is NodesFor's job and is covered, with the real
// service, by group.TestSubscriptionNodesForOnlyNarrows.
func TestVisibleLineIsSelectedByItsOwnLabels(t *testing.T) {
	s, _, u, rule := lineVisibilityFixture(t)
	s.repos.Group = filterGroupRepo{filter: domain.TagFilter{Mode: "any", Tags: []string{"region:US", "tag:cmcc"}}}
	rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "node_set", Value: "tag:cmcc"}}
	preview, err := s.PreviewMihomoGroup(context.Background(), u.GroupID, rule)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A R"}; !reflect.DeepEqual(preview.Groups[0].Members, want) {
		t.Fatalf("visible relay not selected by its tag: %v", preview.Groups[0].Members)
	}
}

func TestTemplatePlaceholdersSkipHiddenLines(t *testing.T) {
	s, _, u, _ := lineVisibilityFixture(t)
	s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{domain.ClientMihomo: strings.Replace(referenceMihomoTemplate, "  {{ proxy_groups }}", "  {{ proxy_groups }}\n  - name: Template\n    type: select\n    proxies:\n      - @all", 1)}}
	output, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output.Body), "A R") {
		t.Fatalf("@all expanded a hidden line:\n%s", output.Body)
	}
}

func TestRouteLineThroughHiddenRelayIsHidden(t *testing.T) {
	s, n, u, rule := lineVisibilityFixture(t)
	n.RouteState.Lines[0].EntryRelayID = "relay"
	rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "node_set", Value: "remaining"}}
	names := renderedNames(t, s, u, domain.ClientMihomo)
	if names["A B"] || names["A R"] {
		t.Fatalf("a route line exposed its hidden entry relay: %v", names)
	}
	if !names["A"] {
		t.Fatalf("direct entry lost: %v", names)
	}
}
