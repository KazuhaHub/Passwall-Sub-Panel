package app

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func (a *App) destinationGroupsOverview(ctx context.Context) ([]destpolicy.GroupAccessSummary, error) {
	ctx, release, err := a.operationGate.Read(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	_, items, _, err := a.readDestinationGroups(ctx, 0)
	return items, err
}

func (a *App) destinationGroupDetail(ctx context.Context, groupID int64) (destpolicy.GroupAccessDetail, error) {
	if groupID <= 0 {
		return destpolicy.GroupAccessDetail{}, domain.ErrValidation
	}
	ctx, release, err := a.operationGate.Read(ctx)
	if err != nil {
		return destpolicy.GroupAccessDetail{}, err
	}
	defer release()
	defs, items, now, err := a.readDestinationGroups(ctx, groupID)
	if err != nil {
		return destpolicy.GroupAccessDetail{}, err
	}
	if len(items) != 1 || a.destGroupTrialRead == nil {
		return destpolicy.GroupAccessDetail{}, domain.ErrUnavailable
	}
	data, err := a.destGroupTrialRead.ReadDestinationGroupTrial(ctx, domain.DestGroupTrialQuery{GroupID: groupID, Since: now.Add(-24 * time.Hour), Until: now})
	if err != nil {
		return destpolicy.GroupAccessDetail{}, err
	}
	readiness, err := destpolicy.BuildGroupReadiness(defs, groupID, items[0].UnappliedNodes, data)
	if err != nil {
		return destpolicy.GroupAccessDetail{}, err
	}
	return destpolicy.GroupAccessDetail{GroupAccessSummary: items[0], Readiness: readiness}, nil
}

// The caller owns backend admission through the complete overview/detail read.
func (a *App) readDestinationGroups(ctx context.Context, groupID int64) (domain.DestDefinitions, []destpolicy.GroupAccessSummary, time.Time, error) {
	bad := func(err error) (domain.DestDefinitions, []destpolicy.GroupAccessSummary, time.Time, error) {
		return domain.DestDefinitions{}, nil, time.Time{}, err
	}
	if a.destDefinitions == nil || a.destGroupStatsRead == nil {
		return bad(domain.ErrUnavailable)
	}
	groups := []*domain.Group{}
	if groupID != 0 {
		group, err := a.repos.Group.GetByID(ctx, groupID)
		if err != nil {
			return bad(err)
		}
		if group == nil || group.ID != groupID {
			return bad(domain.ErrNotFound)
		}
		groups = append(groups, group)
	} else {
		var err error
		groups, err = a.repos.Group.List(ctx)
		if err != nil {
			return bad(err)
		}
	}
	ids := []int64{}
	for _, group := range groups {
		if group == nil || group.ID <= 0 {
			return bad(domain.ErrUnavailable)
		}
		ids = append(ids, group.ID)
	}
	defs, err := a.destDefinitions.ReadDefinitions(ctx)
	if err != nil || destpolicy.CheckDefinitions(defs) != nil {
		return bad(domain.ErrUnavailable)
	}
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return bad(err)
	}
	days := min(7, settings.DestinationSettings().EffectiveTrialRetentionDays())
	members, err := a.repos.Group.CountMembersByGroups(ctx, ids)
	if err != nil {
		return bad(err)
	}
	initial, err := a.destDefinitions.StatusContext(ctx, nil)
	if err != nil {
		return bad(err)
	}
	inputs, groupPanels, err := a.destinationGroupFleetInputs(ctx, initial, ids)
	if err != nil {
		return bad(err)
	}
	related := map[int64]bool{}
	for _, mode := range defs.Groups {
		if mode.Mode == "allowlist" {
			for _, id := range groupPanels[mode.GroupID] {
				related[id] = true
			}
		}
	}
	poll := settings.NodePollSeconds
	if poll <= 0 {
		poll = protocol.DefaultNextPollSeconds
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	applied := map[int64]map[int64]string{}
	current, err := a.destDefinitions.StatusContext(ctx, func(panel domain.DestStatusPanel, load func() ([]byte, error)) (domain.DestCollectionFacts, error) {
		if !related[panel.ID] {
			return domain.DestCollectionFacts{}, nil
		}
		stages, err := destpolicy.ReadAppliedGroupStages(defs.State, panel.DestTestPanel, time.Duration(poll)*time.Second, now, load)
		if err != nil {
			return domain.DestCollectionFacts{}, err
		}
		applied[panel.ID] = stages
		return domain.DestCollectionFacts{}, nil
	})
	if err != nil {
		return bad(err)
	}
	// Eligibility uses the same metadata snapshot as the exact receipt proof.
	// A changed fleet fails the request rather than silently omitting a node.
	panels := map[int64]domain.DestStatusPanel{}
	for _, panel := range current.Panels {
		panels[panel.ID] = panel
	}
	if len(current.Panels) != len(initial.Panels) || len(panels) != len(initial.Panels) {
		return bad(domain.ErrUnavailable)
	}
	for _, panel := range initial.Panels {
		if _, exists := panels[panel.ID]; !exists {
			return bad(domain.ErrUnavailable)
		}
	}
	for i := range inputs {
		panel, exists := panels[inputs[i].Panel.ID]
		if !exists {
			return bad(domain.ErrUnavailable)
		}
		inputs[i].Panel, inputs[i].Quota.Collect, inputs[i].Quota.CollectRevision = panel.DestTestPanel, panel.Collect, panel.CollectRevision
		inputs[i].Engine = ""
		if panel.Agent != nil {
			inputs[i].Engine = string(domain.NormalizeNodeCoreEngine(panel.Agent.DesiredCoreEngine))
		}
	}
	if current.State.Generation != defs.State.Generation || current.State.PublishedGeneration != defs.State.PublishedGeneration || current.State.Paused != defs.State.Paused {
		applied = map[int64]map[int64]string{}
	}
	stats, err := a.destGroupStatsRead.ReadDestinationGroupStats(ctx, domain.DestGroupStatsQuery{GroupIDs: ids, TrialSince: now.Add(-time.Duration(days) * 24 * time.Hour), DeniedSince: now.Add(-24 * time.Hour), Until: now})
	if err != nil {
		return bad(err)
	}
	items, err := destpolicy.BuildGroupOverview(defs, destpolicy.GroupOverviewData{Groups: groups, Members: members, Stats: stats, Nodes: inputs, GroupPanels: groupPanels, AppliedStages: applied, TrialDays: days})
	if err != nil {
		return bad(err)
	}
	if err := ctx.Err(); err != nil {
		return bad(err)
	}
	return defs, items, now, nil
}
