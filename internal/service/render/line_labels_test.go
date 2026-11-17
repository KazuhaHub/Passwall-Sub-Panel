package render

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ADR 0040: each line renders with its own Region/Tags; the direct entry keeps
// the node's. Neither follows the other at render time.
func lineLabelFixture(t *testing.T) (*Service, *domain.Node, *domain.User, *domain.RuleSet) {
	t.Helper()
	s, n, u, rule := referenceRenderFixture(t)
	n.Region, n.Tags = "US", []string{"edge"}
	// Relay "A B" has its labels cleared explicitly; relay "A R" has its own.
	n.Relays[0].Region, n.Relays[0].Tags = "", []string{}
	n.Relays = append(n.Relays, domain.RelayLine{ID: "relay", Name: "R", Address: "relay.example", Enabled: true, Region: "JP", Tags: []string{"cmcc"}})
	return s, n, u, rule
}

func TestNodeSetMembersSelectByEachEntrysOwnLabels(t *testing.T) {
	for selector, want := range map[string][]string{
		"region:US": {"A"},
		"tag:edge":  {"A"},
		"region:JP": {"A R"},
		"tag:cmcc":  {"A R"},
	} {
		t.Run(selector, func(t *testing.T) {
			s, _, u, rule := lineLabelFixture(t)
			u.GroupID = 1
			rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "node_set", Value: selector}}
			preview, err := s.PreviewMihomoGroup(context.Background(), u.GroupID, rule)
			if err != nil || len(preview.Groups) != 1 || !reflect.DeepEqual(preview.Groups[0].Members, want) {
				t.Fatalf("%s selected %+v (%v), want %v", selector, preview, err, want)
			}
		})
	}
}

func TestRelayLineWithClearedLabelsMatchesNoNodeSet(t *testing.T) {
	s, _, u, rule := lineLabelFixture(t)
	u.GroupID = 1
	// The relay line "A B" was cleared explicitly; the node's US/edge must not
	// leak into it through inheritance.
	for _, selector := range []string{"region:US", "tag:edge"} {
		rule.ProxyGroupMembers["国外媒体"] = []domain.ProxyGroupMember{{Kind: "node_set", Value: selector}}
		preview, err := s.PreviewMihomoGroup(context.Background(), u.GroupID, rule)
		if err != nil {
			t.Fatal(err)
		}
		for _, member := range preview.Groups[0].Members {
			if member == "A B" {
				t.Fatalf("%s matched a line through its node's labels", selector)
			}
		}
	}
}

func TestTemplatePlaceholdersUseEntryLabels(t *testing.T) {
	n := &domain.Node{ID: 1, Region: "US", Tags: []string{"edge"}}
	relay := &domain.RelayLine{ID: "r", Region: "JP", Tags: []string{"cmcc"}}
	items := []renderItem{{node: n, name: "direct"}, {node: n, name: "relay", relay: relay}}
	for ref, want := range map[string][]string{
		"@region:US": {"direct"},
		"@region:JP": {"relay"},
		"@tag:edge":  {"direct"},
		"@tag:cmcc":  {"relay"},
	} {
		body := expandNodeRefs("proxies:\n  - "+ref, items)
		var got []string
		for _, line := range strings.Split(body, "\n")[1:] {
			got = append(got, strings.Trim(strings.TrimPrefix(strings.TrimSpace(line), "- "), `"`))
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s expanded to %v, want %v", ref, got, want)
		}
	}
}

func TestInspectionCatalogAndEmptySetCheckIncludeLineLabels(t *testing.T) {
	n := &domain.Node{ID: 1, DisplayName: "A", Enabled: true, Region: "US", Tags: []string{"edge"},
		Relays: []domain.RelayLine{{ID: "r", Address: "relay.example", Enabled: true, Region: "JP", Tags: []string{"cmcc"}}},
	}
	members := map[string][]domain.ProxyGroupMember{"G": {
		{Kind: "node_set", Value: "region:JP"}, {Kind: "node_set", Value: "tag:cmcc"},
		{Kind: "node_set", Value: "region:DE"}, {Kind: "node_set", Value: "tag:retired"},
	}}
	inspection := InspectProxyGroups("- MATCH,G", members, nil, []*domain.Node{n})
	if want := []string{"JP", "US"}; !reflect.DeepEqual(inspection.Regions, want) {
		t.Fatalf("regions %v, want %v", inspection.Regions, want)
	}
	if want := []string{"cmcc", "edge"}; !reflect.DeepEqual(inspection.Tags, want) {
		t.Fatalf("tags %v, want %v", inspection.Tags, want)
	}
	var empty []string
	for _, issue := range inspection.Issues {
		if issue.Code == "empty_node_set" {
			empty = append(empty, issue.Params["value"].(string))
		}
	}
	sort.Strings(empty)
	if want := []string{"region:DE", "tag:retired"}; !reflect.DeepEqual(empty, want) {
		t.Fatalf("empty node sets %v, want %v", empty, want)
	}
	byLine := map[string]ProxyGroupInspectLine{}
	for _, line := range inspection.Lines {
		byLine[line.LineID] = line
	}
	if byLine[domain.DirectLineID].Region != "US" || byLine["r"].Region != "JP" || !reflect.DeepEqual(byLine["r"].Tags, []string{"cmcc"}) {
		t.Fatalf("catalog lines lost their own labels: %+v", inspection.Lines)
	}
}
