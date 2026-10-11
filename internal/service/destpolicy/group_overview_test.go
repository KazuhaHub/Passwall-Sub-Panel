package destpolicy

import (
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func groupOverviewFixture(t *testing.T) (domain.DestDefinitions, GroupOverviewData) {
	t.Helper()
	defs, native := groupCoverageFixture(t)
	base, extra := readyList(2, "full:dns.example.test\n"), readyList(3, "")
	base.OwnerGroupID, extra.OwnerGroupID = 7, 7
	defs.Lists = append(defs.Lists, base, extra)
	defs.Groups[0].BaseListID, defs.Groups[0].ExtraListID = 2, 3
	defs.Groups[0].UpdatedAt = time.UnixMilli(1_800_000_000_000).UTC()
	xui := native
	xui.Panel.ID, xui.Panel.Kind, xui.Panel.Agent = 2, domain.PanelKind3XUI, nil
	return defs, GroupOverviewData{
		Groups:  []*domain.Group{{ID: 8, Name: "Open"}, {ID: 7, Name: "Trial"}, {ID: 9, Name: "Empty"}},
		Members: map[int64]int64{7: 2}, Stats: map[int64]domain.DestGroupStats{7: {TrialSites: 12, TrialCount: 30, DeniedCount: 4}, 8: {}, 9: {}},
		Nodes: []GroupNodeInput{xui, native}, GroupPanels: map[int64][]int64{7: {2, 1}, 8: {2, 1}},
		AppliedStages: map[int64]map[int64]string{1: {7: "trial"}, 2: {7: "trial"}}, TrialDays: 3,
	}
}

func TestGroupOverviewIncludesOpenAndEmptyGroupsAndCountsCurrentStage(t *testing.T) {
	defs, input := groupOverviewFixture(t)
	got, err := BuildGroupOverview(defs, input)
	if err != nil || len(got) != 3 || got[0].GroupID != 7 || got[1].GroupID != 8 || got[2].GroupID != 9 || input.Groups[0].ID != 8 || input.GroupPanels[7][0] != 2 {
		t.Fatalf("group overview order/isolation: %+v %v", got, err)
	}
	trial, open, empty := got[0], got[1], got[2]
	if trial.Members != 2 || trial.TotalNodes != 2 || trial.EligibleNodes != 1 || trial.AppliedNodes != 1 || len(trial.Ineligible) != 1 || trial.Ineligible[0].Reason != "xui" || trial.WouldDenyRecent.Days != 3 || trial.WouldDenyRecent.Sites != 12 || trial.WouldDenyRecent.Count != 30 || trial.Denied24h != 4 || trial.UpdatedAt != 1_800_000_000_000 || len(trial.UnappliedNodes) != 0 {
		t.Fatal("allowlist overview used open-mode eligibility or lost current counters/version")
	}
	if open.Mode != "open" || open.Members != 0 || open.EligibleNodes != 2 || open.AppliedNodes != 0 || len(open.Ineligible) != 0 || open.ListIDs == nil || empty.TotalNodes != 0 || empty.Ineligible == nil {
		t.Fatal("open/empty groups were omitted or inherited allowlist exclusions")
	}
	input.AppliedStages[1][7] = "enforce"
	got, err = BuildGroupOverview(defs, input)
	if err != nil || got[0].AppliedNodes != 0 || len(got[0].UnappliedNodes) != 1 || got[0].UnappliedNodes[0].PanelID != 1 {
		t.Fatal("same group ID from a different stage counted as applied")
	}
}

func TestGroupOverviewWithholdsAllGroupsOnCorruption(t *testing.T) {
	for _, change := range []func(*domain.DestDefinitions, *GroupOverviewData){
		func(_ *domain.DestDefinitions, d *GroupOverviewData) { d.Groups = append(d.Groups, d.Groups[0]) },
		func(_ *domain.DestDefinitions, d *GroupOverviewData) { d.Members[7] = -1 },
		func(_ *domain.DestDefinitions, d *GroupOverviewData) { delete(d.Stats, 8) },
		func(_ *domain.DestDefinitions, d *GroupOverviewData) {
			d.Stats[7] = domain.DestGroupStats{TrialSites: -1}
		},
		func(_ *domain.DestDefinitions, d *GroupOverviewData) { d.GroupPanels[7] = []int64{999} },
		func(_ *domain.DestDefinitions, d *GroupOverviewData) { d.GroupPanels[7] = []int64{1, 1} },
		func(_ *domain.DestDefinitions, d *GroupOverviewData) { d.Nodes = append(d.Nodes, d.Nodes[0]) },
		func(_ *domain.DestDefinitions, d *GroupOverviewData) { d.TrialDays = 0 },
		func(defs *domain.DestDefinitions, _ *GroupOverviewData) { defs.Lists[1].OwnerGroupID = 8 },
	} {
		defs, input := groupOverviewFixture(t)
		change(&defs, &input)
		got, err := BuildGroupOverview(defs, input)
		if err == nil || got != nil {
			t.Fatal("corrupt overview returned partial groups")
		}
	}
}

func TestGroupReadinessKeepsListReadinessSeparateFromWarningsAndObservedSites(t *testing.T) {
	defs, _ := groupOverviewFixture(t)
	pending := domain.DestList{ID: 4, Name: "Pending", Kind: domain.DestListRemote, SourceURL: "https://example.test/list"}
	defs.Lists = append(defs.Lists, pending)
	defs.Groups[0].ListIDs = append(defs.Groups[0].ListIDs, 4)
	data := domain.DestGroupTrialData{Items: []domain.DestGroupTrialSite{
		{Site: "example.test", Count: 100, LastAt: 10}, {Site: "last.test", Count: 1, LastAt: 10},
		{Site: "third.test", Count: 5, LastAt: 10}, {Site: "first.test", Count: 20, LastAt: 10}, {Site: "(ip)", Count: 4, LastAt: 10},
	}, DroppedInRange: 2, Losses: domain.DestAuditLosses{Scope: "panel", Rows: 2}}
	unapplied := []GroupNodeReference{{PanelID: 1, Name: "Unapplied"}}
	got, err := BuildGroupReadiness(defs, 7, unapplied, data)
	if err != nil || got.ListsReady || len(got.NotReadyLists) != 1 || got.NotReadyLists[0].ID != 4 || !got.BaseNonempty || got.RemainingSites24h != 4 || len(got.TopSites) != 3 || got.TopSites[0].Site != "first.test" || got.TopSites[1].Site != "third.test" || got.TopSites[2].Site != "(ip)" || !reflect.DeepEqual(got.UnappliedNodes, unapplied) || got.TrialLosses.Rows != 2 {
		t.Fatalf("readiness lost blockers/warnings or current whole-site coverage: %+v %v", got, err)
	}
	fetched := time.UnixMilli(1_800_000_000_000).UTC()
	defs.Lists[len(defs.Lists)-1].LastFetchedAt, defs.Lists[len(defs.Lists)-1].LastError = &fetched, "later refresh failed"
	defs.Lists[1] = readyList(2, "")
	defs.Lists[1].OwnerGroupID = 7
	got, err = BuildGroupReadiness(defs, 7, nil, data)
	if err != nil || !got.ListsReady || got.BaseNonempty || got.NotReadyLists == nil || got.UnappliedNodes == nil {
		t.Fatal("last-good list or deliberately empty base became an enforcement blocker")
	}
	data.Losses.Complete = true
	if _, err := BuildGroupReadiness(defs, 7, nil, data); err == nil {
		t.Fatal("invalid collection loss proof accepted")
	}
}
