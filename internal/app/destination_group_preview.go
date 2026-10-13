package app

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func (a *App) destinationGroupPreview(ctx context.Context, groupID int64, listIDs []int64) (destpolicy.GroupAccessPreview, error) {
	if groupID <= 0 {
		return destpolicy.GroupAccessPreview{}, domain.ErrValidation
	}
	ctx, release, err := a.operationGate.Read(ctx)
	if err != nil {
		return destpolicy.GroupAccessPreview{}, err
	}
	defer release()
	if a.destGroups == nil || a.destTagMembers == nil {
		return destpolicy.GroupAccessPreview{}, domain.ErrUnavailable
	}
	if _, err := a.repos.Group.GetByID(ctx, groupID); err != nil {
		return destpolicy.GroupAccessPreview{}, err
	}
	mode, err := a.destDefinitions.GetGroupMode(ctx, groupID)
	if err != nil {
		return destpolicy.GroupAccessPreview{}, err
	}
	hosts, available := []string{}, true
	input := destpolicy.GroupModeInput{Mode: "allowlist", Stage: "trial", ListIDs: listIDs}
	if mode.BaseListID == 0 {
		hosts, err = a.destinationGroupBaseHosts(ctx)
		available = err == nil
		if !available {
			hosts = []string{}
		}
		input.BaseHosts = hosts
	}
	prepared, err := a.destGroups.Preview(ctx, groupID, input)
	if err != nil {
		return destpolicy.GroupAccessPreview{}, err
	}
	members, err := a.repos.Group.CountMembers(ctx, groupID)
	if err != nil || members < 0 {
		return destpolicy.GroupAccessPreview{}, domain.ErrUnavailable
	}
	panels, err := a.destinationGroupNodeInputs(ctx, groupID)
	if err != nil {
		return destpolicy.GroupAccessPreview{}, err
	}
	coverage, err := destpolicy.AssessGroupNodes(prepared.Definitions, groupID, panels)
	if err != nil {
		return destpolicy.GroupAccessPreview{}, err
	}
	return destpolicy.GroupAccessPreview{GroupNodeCoverage: coverage, Members: members, BaseHosts: hosts, BaseHostsAvailable: available, BaseListID: mode.BaseListID, ExtraListID: mode.ExtraListID, Budget: prepared.Budget}, nil
}

func (a *App) destinationGroupNodeInputs(ctx context.Context, groupID int64) ([]destpolicy.GroupNodeInput, error) {
	current, err := a.destDefinitions.StatusContext(ctx, nil)
	if err != nil {
		return nil, err
	}
	inputs, _, err := a.destinationGroupFleetInputs(ctx, current, []int64{groupID})
	return inputs, err
}

func (a *App) destinationGroupFleetInputs(ctx context.Context, current domain.DestStatusContext, groupIDs []int64) ([]destpolicy.GroupNodeInput, map[int64][]int64, error) {
	if a.destTagMembers == nil || a.destGroupListeners == nil {
		return nil, nil, domain.ErrUnavailable
	}
	wanted := map[int64]bool{}
	groupPanels := map[int64][]int64{}
	for _, id := range groupIDs {
		if id <= 0 {
			return nil, nil, domain.ErrValidation
		}
		wanted[id], groupPanels[id] = true, []int64{}
	}
	nodes, err := a.destGroupListeners.ReadDestinationGroupListeners(ctx)
	if err != nil {
		return nil, nil, err
	}
	listeners := map[int64][]protocol.Listener{}
	nodeIDs := map[int64]bool{}
	for _, node := range nodes {
		if node.NodeID <= 0 || node.PanelID <= 0 || nodeIDs[node.NodeID] {
			return nil, nil, domain.ErrUnavailable
		}
		nodeIDs[node.NodeID] = true
		// These are the same two ConfigIntent fields consumed by PreflightPolicy.
		// No listener credentials or transport configuration enter the preview.
		raw, err := json.Marshal(struct {
			Enabled  bool   `json:"enabled"`
			Sniffing string `json:"sniffing"`
		}{true, node.Sniffing})
		if err != nil {
			return nil, nil, domain.ErrUnavailable
		}
		listeners[node.PanelID] = append(listeners[node.PanelID], protocol.Listener{Key: protocol.NewListenerKey(node.NodeID), Config: protocol.RawConfig(raw)})
	}
	result := []destpolicy.GroupNodeInput{}
	panelIDs := map[int64]bool{}
	for _, panel := range current.Panels {
		if panel.ID <= 0 || panelIDs[panel.ID] {
			return nil, nil, domain.ErrUnavailable
		}
		panelIDs[panel.ID] = true
		members, err := a.destTagMembers.TagMatchedMembers(ctx, panel.ID)
		if err != nil {
			return nil, nil, err
		}
		matched := []int64{}
		for id := range members {
			if wanted[id] {
				matched = append(matched, id)
			}
		}
		if len(matched) == 0 {
			continue
		}
		if len(listeners[panel.ID]) == 0 {
			return nil, nil, domain.ErrUnavailable
		}
		quota := destpolicy.RosterInput{UserGroups: map[int64]int64{}, Collect: panel.Collect, CollectRevision: panel.CollectRevision}
		for id, users := range members {
			if id <= 0 {
				return nil, nil, domain.ErrUnavailable
			}
			for _, user := range users {
				if user <= 0 {
					return nil, nil, domain.ErrUnavailable
				}
				if old, exists := quota.UserGroups[user]; exists && old != id {
					return nil, nil, domain.ErrUnavailable
				}
				quota.UserGroups[user] = id
				quota.UserIDs = append(quota.UserIDs, user)
			}
		}
		slices.Sort(quota.UserIDs)
		quota.UserIDs = slices.Compact(quota.UserIDs)
		input := destpolicy.GroupNodeInput{Panel: panel.DestTestPanel, Quota: quota, Listeners: listeners[panel.ID]}
		if panel.Agent != nil {
			input.Engine = string(domain.NormalizeNodeCoreEngine(panel.Agent.DesiredCoreEngine))
		}
		result = append(result, input)
		for _, id := range matched {
			groupPanels[id] = append(groupPanels[id], panel.ID)
		}
	}
	return result, groupPanels, nil
}
