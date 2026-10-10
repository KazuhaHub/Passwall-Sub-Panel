package sqlstore

import (
	"context"
	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"slices"
)

// StatusContext keeps every fleet metadata read and lazy candidate proof in
// one repeatable-read transaction. Bodies never escape this projection.
func (r *DestDefinitionRepo) StatusContext(ctx context.Context, proof func(domain.DestStatusPanel, func() ([]byte, error)) (domain.DestCollectionFacts, error)) (domain.DestStatusContext, error) {
	var result domain.DestStatusContext
	err := r.readTransaction(ctx, func(tx *gorm.DB) error {
		var err error
		result, err = readDestStatusContext(tx, proof, true)
		return err
	})
	if err != nil {
		return domain.DestStatusContext{}, err
	}
	return result, nil
}

// The caller owns the transaction. Disclosure reads reuse the current proof
// projection without reading server/group names, listeners or historical data.
func readDestStatusContext(tx *gorm.DB, proof func(domain.DestStatusPanel, func() ([]byte, error)) (domain.DestCollectionFacts, error), details bool) (domain.DestStatusContext, error) {
	result := domain.DestStatusContext{Panels: []domain.DestStatusPanel{}, GroupNames: map[int64]string{}}
	err := func() error {
		var err error
		if details {
			result.State, err = readDestState(tx)
			if err != nil {
				return err
			}
		}
		var panels []xuiPanelRow
		panelColumns := []string{"id", "name", "kind", "panel_version", "audit_collect", "audit_collect_revision"}
		if !details {
			panelColumns = []string{"id", "kind", "audit_collect", "audit_collect_revision"}
		}
		if err := tx.Select(panelColumns).Order("id").Find(&panels).Error; err != nil {
			return err
		}
		ids := []int64{}
		byPanel := map[int64]int{}
		for _, p := range panels {
			kind := domain.NormalizePanelKind(domain.PanelKind(p.Kind))
			entry := domain.DestStatusPanel{DestTestPanel: domain.DestTestPanel{ID: p.ID, Name: p.Name, Kind: kind}, Version: p.PanelVersion, Collect: domain.AuditCollect(p.AuditCollect), Listeners: map[string]domain.DestStatusListener{}}
			if kind == domain.PanelKindPSP {
				if !entry.Collect.Valid() || p.AuditCollectRevision < 1 {
					return domain.ErrUnavailable
				}
				entry.CollectRevision = uint64(p.AuditCollectRevision)
				ids = append(ids, p.ID)
			} else {
				entry.Collect = domain.AuditCollectOff
			}
			byPanel[p.ID] = len(result.Panels)
			result.Panels = append(result.Panels, entry)
		}
		for start := 0; start < len(ids); start += 512 {
			var agents []nodeAgentRow
			if err := tx.Select("agent_id", "panel_id", "observed_capabilities", "observed_core_engine", "last_seen").Where("panel_id IN ?", ids[start:min(start+512, len(ids))]).Find(&agents).Error; err != nil {
				return err
			}
			agentIDs := []string{}
			byAgent := map[string]int{}
			for _, a := range agents {
				i := byPanel[a.PanelID]
				result.Panels[i].Agent = &domain.NodeAgent{AgentID: a.AgentID, PanelID: a.PanelID, ObservedCapabilities: append([]string(nil), a.ObservedCapabilities...), ObservedCoreEngine: domain.NodeCoreEngine(a.ObservedCoreEngine), LastSeen: runtimeTimeCopy(a.LastSeen)}
				agentIDs = append(agentIDs, a.AgentID)
				byAgent[a.AgentID] = i
			}
			if len(agentIDs) == 0 {
				continue
			}
			var runtimes []destAgentPolicyRow
			runtimeRead := tx.Omit("MintedBody", "AppliedBody")
			if !details {
				runtimeRead = tx.Select("agent_id", "minted_sha256", "minted_at", "reported_sha256", "reported_state", "fallback_exhausted")
			}
			if err := runtimeRead.Where("agent_id IN ?", agentIDs).Find(&runtimes).Error; err != nil {
				return err
			}
			for _, row := range runtimes {
				runtime, err := runtimePolicyDomain(row)
				if err != nil {
					return err
				}
				result.Panels[byAgent[row.AgentID]].Runtime = runtime
			}
		}
		groupIDs := []int64{}
		for i := range result.Panels {
			p := &result.Panels[i]
			runtime := p.Runtime
			if runtime == nil {
				continue
			}
			if details {
				groupIDs = append(groupIDs, runtime.AppliedGroups...)
			}
			if details && len(runtime.PrecheckListeners)+len(runtime.ReportedListeners) > 0 {
				var nodes []nodeRow
				if err := tx.Select("id", "display_name").Where("panel_id = ?", p.ID).Order("id").Find(&nodes).Error; err != nil {
					return err
				}
				for _, node := range nodes {
					id := node.ID
					key := string(protocol.NewListenerKey(id))
					p.Listeners[key] = domain.DestStatusListener{Listener: key, Label: node.DisplayName, NodeID: &id}
				}
			}
			if proof != nil {
				facts, err := proof(*p, func() ([]byte, error) {
					var row destAgentPolicyRow
					if err := tx.Select("minted_body").Where("agent_id = ? AND minted_sha256 = ?", runtime.AgentID, runtime.MintedSHA256).First(&row).Error; err != nil {
						return nil, err
					}
					return []byte(row.MintedBody), nil
				})
				if err != nil {
					return err
				}
				p.Facts = facts
			}
		}
		slices.Sort(groupIDs)
		groupIDs = slices.Compact(groupIDs)
		for start := 0; start < len(groupIDs); start += 512 {
			var groups []groupRow
			if err := tx.Select("id", "name").Where("id IN ?", groupIDs[start:min(start+512, len(groupIDs))]).Find(&groups).Error; err != nil {
				return err
			}
			for _, g := range groups {
				result.GroupNames[g.ID] = g.Name
			}
		}
		return nil
	}()
	if err != nil {
		return domain.DestStatusContext{}, err
	}
	return result, nil
}
