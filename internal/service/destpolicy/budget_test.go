package destpolicy

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestDefinitionBudgetExpandsSegmentsAndUsesLargestTagMatchedPanel(t *testing.T) {
	defs := domain.DestDefinitions{State: domain.DestPolicyState{Paused: true}, Lists: []domain.DestList{readyList(1, "domain:example.test\nregexp:^host[0-9]+\\.example\\.test$\n")}, Policies: []domain.DestPolicy{
		{ID: 1, Action: domain.DestBlock, Enabled: true, Scope: domain.DestScopeGroups, GroupIDs: []int64{7}, ListIDs: []int64{1}, Inline: domain.DestInline{Protocols: []string{"bittorrent"}}},
		{ID: 2, Action: domain.DestBlock, Enabled: false, Scope: domain.DestScopeAll, Inline: domain.DestInline{Ports: "25"}},
	}, Groups: []domain.DestGroupMode{{GroupID: 7, Mode: "allowlist", Stage: "trial", BaseListID: 1}}}
	panels := []RosterInput{{UserIDs: []int64{10}, UserGroups: map[int64]int64{10: 7}}, {UserIDs: []int64{10, 11, 12}, UserGroups: map[int64]int64{10: 7, 11: 7, 12: 7}}}
	budget, err := DefinitionBudget(defs, panels)
	if err != nil {
		t.Fatal(err)
	}
	if budget.Rules.Used != 4 || budget.Domains.Used != 4 || budget.Regexps.Used != 2 || budget.Subjects.Used != 12 || budget.Bytes.Used <= 0 {
		t.Fatalf("expanded budget=%+v", budget)
	}
	empty, err := DefinitionBudget(defs, nil)
	if err != nil || empty.Rules != budget.Rules || empty.Bytes != budget.Bytes || empty.Subjects.Used != 0 {
		t.Fatal("definition quota depended on panel roster or pause")
	}
}
