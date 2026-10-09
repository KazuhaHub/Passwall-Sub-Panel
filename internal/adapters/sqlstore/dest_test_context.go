package sqlstore

import (
	"context"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

// TestContext reads publication, client scope, current group membership and
// display/status metadata in one repeatable-read transaction. It never invokes
// the sync compiler, resolves entitlements, or reads secrets/config bodies.
func (r *DestDefinitionRepo) TestContext(ctx context.Context, userID, panelID int64) (domain.DestTestContext, error) {
	if userID < 0 || panelID < 0 {
		return domain.DestTestContext{}, domain.ErrValidation
	}
	result := domain.DestTestContext{UserIDs: []int64{}, UserGroups: map[int64]int64{}, PolicyNames: map[int64]string{}, GroupNames: map[int64]string{}, Panels: []domain.DestTestPanel{}}
	err := r.readTransaction(ctx, func(tx *gorm.DB) error {
		var err error
		result.State, err = readDestState(tx)
		if err != nil {
			return err
		}
		result.Snapshot, result.Published, err = readDestPublishedSnapshot(tx, result.State)
		if err != nil {
			return err
		}
		if userID > 0 {
			var user userRow
			if err := tx.Select("id", "group_id").First(&user, "id = ?", userID).Error; err != nil {
				return destinationRowError(err)
			}
		}
		query := tx.Select("id", "name", "kind").Order("id")
		if panelID > 0 {
			query = query.Where("id = ?", panelID)
		} else if userID > 0 {
			query = query.Where("id IN (?)", tx.Model(&pspClientRow{}).Select("panel_id").Where("user_id = ?", userID))
		}
		var panels []xuiPanelRow
		if err := query.Find(&panels).Error; err != nil {
			return err
		}
		if panelID > 0 && len(panels) != 1 {
			return domain.ErrNotFound
		}
		ids := make([]int64, len(panels))
		byPanel := map[int64]int{}
		for i, p := range panels {
			kind := domain.NormalizePanelKind(domain.PanelKind(p.Kind))
			result.Panels = append(result.Panels, domain.DestTestPanel{ID: p.ID, Name: p.Name, Kind: kind})
			ids[i], byPanel[p.ID] = p.ID, i
			if p.ID == panelID || panelID == 0 && userID > 0 && kind == domain.PanelKindPSP && result.SelectedPanelID == 0 {
				result.SelectedPanelID = p.ID
			}
		}
		for start := 0; start < len(ids); start += 512 {
			end := min(start+512, len(ids))
			var agents []nodeAgentRow
			if err := tx.Select("agent_id", "panel_id", "observed_capabilities", "observed_core_engine", "last_seen").Where("panel_id IN ?", ids[start:end]).Find(&agents).Error; err != nil {
				return err
			}
			agentIDs := make([]string, 0, len(agents))
			byAgent := map[string]int{}
			for _, a := range agents {
				i := byPanel[a.PanelID]
				result.Panels[i].Agent = &domain.NodeAgent{AgentID: a.AgentID, PanelID: a.PanelID, ObservedCapabilities: append([]string(nil), a.ObservedCapabilities...), ObservedCoreEngine: domain.NodeCoreEngine(a.ObservedCoreEngine), LastSeen: runtimeTimeCopy(a.LastSeen)}
				agentIDs, byAgent[a.AgentID] = append(agentIDs, a.AgentID), i
			}
			if len(agentIDs) == 0 {
				continue
			}
			var runtimes []destAgentPolicyRow
			if err := tx.Omit("MintedBody", "AppliedBody").Where("agent_id IN ?", agentIDs).Find(&runtimes).Error; err != nil {
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
		if result.SelectedPanelID > 0 {
			if err := tx.Model(&pspClientRow{}).Distinct("user_id").Where("panel_id = ?", result.SelectedPanelID).Order("user_id").Pluck("user_id", &result.UserIDs).Error; err != nil {
				return err
			}
			for start := 0; start < len(result.UserIDs); start += 512 {
				end := min(start+512, len(result.UserIDs))
				var users []userRow
				if err := tx.Select("id", "group_id").Where("id IN ?", result.UserIDs[start:end]).Find(&users).Error; err != nil {
					return err
				}
				for _, user := range users {
					result.UserGroups[user.ID] = user.GroupID
				}
			}
		}
		var policies []destPolicyRow
		if err := tx.Select("id", "name").Find(&policies).Error; err != nil {
			return err
		}
		for _, p := range policies {
			result.PolicyNames[p.ID] = p.Name
		}
		var groups []groupRow
		if err := tx.Select("id", "name").Find(&groups).Error; err != nil {
			return err
		}
		for _, g := range groups {
			result.GroupNames[g.ID] = g.Name
		}
		return nil
	})
	if err != nil {
		return domain.DestTestContext{}, err
	}
	return result, nil
}
