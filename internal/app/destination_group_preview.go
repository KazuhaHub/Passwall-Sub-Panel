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
	if a.destTagMembers == nil {
		return nil, domain.ErrUnavailable
	}
	current, err := a.destDefinitions.StatusContext(ctx, nil)
	if err != nil {
		return nil, err
	}
	nodes, err := a.repos.Node.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	listeners := map[int64][]protocol.Listener{}
	for _, node := range nodes {
		if node == nil || node.ID <= 0 || node.PanelID <= 0 {
			return nil, domain.ErrUnavailable
		}
		// These are the same two ConfigIntent fields consumed by PreflightPolicy.
		// No listener credentials or transport configuration enter the preview.
		raw, err := json.Marshal(struct {
			Enabled  bool   `json:"enabled"`
			Sniffing string `json:"sniffing"`
		}{node.Enabled, node.Sniffing})
		if err != nil {
			return nil, domain.ErrUnavailable
		}
		listeners[node.PanelID] = append(listeners[node.PanelID], protocol.Listener{Key: protocol.NewListenerKey(node.ID), Config: protocol.RawConfig(raw)})
	}
	result := []destpolicy.GroupNodeInput{}
	for _, panel := range current.Panels {
		members, err := a.destTagMembers.TagMatchedMembers(ctx, panel.ID)
		if err != nil {
			return nil, err
		}
		if _, matched := members[groupID]; !matched {
			continue
		}
		if len(listeners[panel.ID]) == 0 {
			return nil, domain.ErrUnavailable
		}
		quota := destpolicy.RosterInput{UserGroups: map[int64]int64{}, Collect: panel.Collect, CollectRevision: panel.CollectRevision}
		for id, users := range members {
			if id <= 0 {
				return nil, domain.ErrUnavailable
			}
			for _, user := range users {
				if user <= 0 {
					return nil, domain.ErrUnavailable
				}
				if old, exists := quota.UserGroups[user]; exists && old != id {
					return nil, domain.ErrUnavailable
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
	}
	return result, nil
}
