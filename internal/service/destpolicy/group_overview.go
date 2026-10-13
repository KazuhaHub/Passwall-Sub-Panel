package destpolicy

import (
	"cmp"
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type GroupNodeReference struct {
	PanelID int64  `json:"panel_id"`
	Name    string `json:"name"`
}

type GroupListReference struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type GroupTrialCounters struct {
	Days  int   `json:"days"`
	Sites int64 `json:"sites"`
	Count int64 `json:"count"`
}

type GroupAccessSummary struct {
	GroupNodeCoverage
	GroupID         int64                `json:"group_id"`
	GroupName       string               `json:"group_name"`
	Members         int64                `json:"members"`
	Mode            string               `json:"mode"`
	Stage           string               `json:"stage"`
	StageChangedAt  *int64               `json:"stage_changed_at"`
	UpdatedAt       int64                `json:"updated_at"`
	ListIDs         []int64              `json:"list_ids"`
	BaseListID      int64                `json:"base_list_id"`
	ExtraListID     int64                `json:"extra_list_id"`
	AppliedNodes    int                  `json:"applied_nodes"`
	WouldDenyRecent GroupTrialCounters   `json:"would_deny_recent"`
	Denied24h       int64                `json:"denied_24h"`
	UnappliedNodes  []GroupNodeReference `json:"-"`
}

type GroupAccessReadiness struct {
	ListsReady        bool                   `json:"lists_ready"`
	NotReadyLists     []GroupListReference   `json:"not_ready_lists"`
	UnappliedNodes    []GroupNodeReference   `json:"unapplied_nodes"`
	BaseNonempty      bool                   `json:"base_nonempty"`
	RemainingSites24h int64                  `json:"remaining_sites_24h"`
	TopSites          []GroupTrialReportSite `json:"top_sites"`
	TrialLosses       domain.DestAuditLosses `json:"trial_losses"`
}

type GroupAccessDetail struct {
	GroupAccessSummary
	Readiness GroupAccessReadiness `json:"readiness"`
}

type GroupOverviewData struct {
	Groups        []*domain.Group
	Members       map[int64]int64
	Stats         map[int64]domain.DestGroupStats
	Nodes         []GroupNodeInput
	GroupPanels   map[int64][]int64
	AppliedStages map[int64]map[int64]string
	TrialDays     int
}

func groupOverviewMode(defs domain.DestDefinitions, groupID int64) domain.DestGroupMode {
	for _, mode := range defs.Groups {
		if mode.GroupID == groupID {
			return mode
		}
	}
	return domain.DestGroupMode{GroupID: groupID, Mode: "open"}
}

func groupOverviewLists(lists map[int64]domain.DestList, mode domain.DestGroupMode) ([]domain.DestList, error) {
	if _, err := domain.DestinationGroupAccessMode(mode.Mode, mode.Stage); err != nil || mode.BaseListID < 0 || mode.ExtraListID < 0 || mode.BaseListID > 0 && mode.BaseListID == mode.ExtraListID || mode.Mode == "allowlist" && (mode.BaseListID == 0 || mode.ExtraListID == 0) {
		return nil, domain.ErrUnavailable
	}
	for _, id := range positiveIDs(mode.BaseListID, mode.ExtraListID) {
		list, exists := lists[id]
		if !exists || list.Kind != domain.DestListCustom || list.OwnerGroupID != mode.GroupID {
			return nil, domain.ErrUnavailable
		}
	}
	ids := append(slices.Clone(mode.ListIDs), positiveIDs(mode.BaseListID, mode.ExtraListID)...)
	selected := []domain.DestList{}
	for _, id := range uniqueIDs(ids) {
		list, exists := lists[id]
		if id <= 0 || !exists || list.OwnerGroupID != 0 && list.OwnerGroupID != mode.GroupID {
			return nil, domain.ErrUnavailable
		}
		selected = append(selected, list)
	}
	return selected, nil
}

// Inputs share one fleet projection. Each node's prospective policy is checked
// at most once; open groups retain their current tag-matching semantics.
func BuildGroupOverview(defs domain.DestDefinitions, data GroupOverviewData) ([]GroupAccessSummary, error) {
	if data.TrialDays < 1 || data.TrialDays > 30 || CheckDefinitions(defs) != nil {
		return nil, domain.ErrUnavailable
	}
	lists := map[int64]domain.DestList{}
	for _, list := range defs.Lists {
		lists[list.ID] = list
	}
	nodes := map[int64]GroupNodeInput{}
	for _, input := range data.Nodes {
		if input.Panel.ID <= 0 {
			return nil, domain.ErrUnavailable
		}
		if _, duplicate := nodes[input.Panel.ID]; duplicate {
			return nil, domain.ErrUnavailable
		}
		switch domain.NormalizePanelKind(input.Panel.Kind) {
		case domain.PanelKindPSP, domain.PanelKind3XUI, domain.PanelKindSUI:
		default:
			return nil, domain.ErrUnavailable
		}
		nodes[input.Panel.ID] = input
	}
	groups := slices.Clone(data.Groups)
	for _, group := range groups {
		if group == nil || group.ID <= 0 {
			return nil, domain.ErrUnavailable
		}
	}
	slices.SortFunc(groups, func(a, b *domain.Group) int { return cmp.Compare(a.ID, b.ID) })
	check := defs
	check.State.Paused = false
	exclusions := map[int64]string{}
	out := []GroupAccessSummary{}
	var previous int64
	for _, group := range groups {
		if group.ID == previous {
			return nil, domain.ErrUnavailable
		}
		previous = group.ID
		mode := groupOverviewMode(defs, group.ID)
		if _, err := groupOverviewLists(lists, mode); err != nil {
			return nil, err
		}
		stats, known := data.Stats[group.ID]
		if !known || data.Members[group.ID] < 0 || stats.TrialSites < 0 || stats.TrialCount < 0 || stats.DeniedCount < 0 {
			return nil, domain.ErrUnavailable
		}
		item := GroupAccessSummary{GroupID: group.ID, GroupName: group.Name, Members: data.Members[group.ID], Mode: mode.Mode, Stage: mode.Stage, ListIDs: slices.Clone(mode.ListIDs), BaseListID: mode.BaseListID, ExtraListID: mode.ExtraListID, WouldDenyRecent: GroupTrialCounters{Days: data.TrialDays, Sites: stats.TrialSites, Count: stats.TrialCount}, Denied24h: stats.DeniedCount, UnappliedNodes: []GroupNodeReference{}, GroupNodeCoverage: GroupNodeCoverage{Ineligible: []GroupIneligibleNode{}, EligiblePanelIDs: []int64{}}}
		if item.ListIDs == nil {
			item.ListIDs = []int64{}
		}
		if !mode.UpdatedAt.IsZero() {
			item.UpdatedAt = mode.UpdatedAt.UnixMilli()
		}
		if mode.StageChangedAt != nil {
			at := mode.StageChangedAt.UnixMilli()
			item.StageChangedAt = &at
		}
		if item.UpdatedAt < 0 || item.StageChangedAt != nil && *item.StageChangedAt <= 0 {
			return nil, domain.ErrUnavailable
		}
		ids := slices.Clone(data.GroupPanels[group.ID])
		slices.Sort(ids)
		var lastID int64
		for _, id := range ids {
			input, exists := nodes[id]
			if !exists || id <= lastID {
				return nil, domain.ErrUnavailable
			}
			lastID = id
			item.TotalNodes++
			reason := ""
			if mode.Mode == "allowlist" {
				var cached bool
				reason, cached = exclusions[id]
				if !cached {
					var err error
					reason, err = groupNodeExclusion(check, input)
					if err != nil {
						return nil, err
					}
					exclusions[id] = reason
				}
			}
			if reason != "" {
				item.Ineligible = append(item.Ineligible, GroupIneligibleNode{PanelID: id, Name: input.Panel.Name, Reason: reason})
				continue
			}
			item.EligibleNodes++
			item.EligiblePanelIDs = append(item.EligiblePanelIDs, id)
			if mode.Mode == "allowlist" {
				if data.AppliedStages[id][group.ID] == mode.Stage {
					item.AppliedNodes++
				} else {
					item.UnappliedNodes = append(item.UnappliedNodes, GroupNodeReference{PanelID: id, Name: input.Panel.Name})
				}
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func BuildGroupReadiness(defs domain.DestDefinitions, groupID int64, unapplied []GroupNodeReference, data domain.DestGroupTrialData) (GroupAccessReadiness, error) {
	if groupID <= 0 || CheckDefinitions(defs) != nil {
		return GroupAccessReadiness{}, domain.ErrUnavailable
	}
	mode := groupOverviewMode(defs, groupID)
	lists := map[int64]domain.DestList{}
	for _, list := range defs.Lists {
		lists[list.ID] = list
	}
	selected, err := groupOverviewLists(lists, mode)
	if err != nil {
		return GroupAccessReadiness{}, err
	}
	coverage, err := groupTrialCoverage(defs, groupID)
	if err != nil {
		return GroupAccessReadiness{}, err
	}
	report, err := groupTrialReportData(coverage, 1, data)
	if err != nil {
		return GroupAccessReadiness{}, err
	}
	out := GroupAccessReadiness{ListsReady: true, NotReadyLists: []GroupListReference{}, UnappliedNodes: slices.Clone(unapplied), BaseNonempty: lists[mode.BaseListID].EntryCount > 0, TopSites: []GroupTrialReportSite{}, TrialLosses: report.Losses}
	if out.UnappliedNodes == nil {
		out.UnappliedNodes = []GroupNodeReference{}
	}
	for _, list := range selected {
		if list.Kind != domain.DestListCustom && list.LastFetchedAt == nil {
			out.ListsReady = false
			out.NotReadyLists = append(out.NotReadyLists, GroupListReference{ID: list.ID, Name: list.Name})
		}
	}
	remaining := []GroupTrialReportSite{}
	for _, item := range report.Items {
		if !item.Added {
			remaining = append(remaining, item)
		}
	}
	out.RemainingSites24h = int64(len(remaining))
	slices.SortFunc(remaining, func(a, b GroupTrialReportSite) int {
		if v := cmp.Compare(b.Count, a.Count); v != 0 {
			return v
		}
		if v := cmp.Compare(b.LastAt, a.LastAt); v != 0 {
			return v
		}
		return cmp.Compare(a.Site, b.Site)
	})
	out.TopSites = append(out.TopSites, remaining[:min(3, len(remaining))]...)
	return out, nil
}
