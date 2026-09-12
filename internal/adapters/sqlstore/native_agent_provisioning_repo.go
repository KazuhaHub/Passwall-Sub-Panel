package sqlstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type nativeAgentProvisioningRepo struct{ db *gorm.DB }

func (r *nativeAgentProvisioningRepo) Create(ctx context.Context, panel *domain.XUIPanel, agent *domain.NodeAgent) error {
	if panel == nil || agent == nil || panel.ID != 0 || agent.ID != 0 || agent.PanelID != 0 {
		return errors.New("provision native agent: new panel and agent are required")
	}
	if domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP || panel.URL != "psp://"+agent.AgentID {
		return errors.New("provision native agent: invalid native panel identity")
	}
	if panel.APIToken != "" || panel.Username != "" || panel.Password != "" || panel.InsecureSkipVerify {
		return errors.New("provision native agent: native panel cannot carry upstream credentials")
	}
	copyAgent := *agent
	copyAgent.PanelID = 1 // validate shape before the real panel ID is allocated.
	if err := validateNewNodeAgent(&copyAgent); err != nil {
		return err
	}
	panelRow, err := xuiPanelFromDomain(panel)
	if err != nil {
		return err
	}
	var agentRow *nodeAgentRow
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(panelRow).Error; err != nil {
			return err
		}
		copyAgent.PanelID = panelRow.ID
		agentRow, err = createNodeAgentRows(tx, &copyAgent)
		return err
	})
	if err != nil {
		return err
	}
	panel.ID = panelRow.ID
	agent.PanelID = panelRow.ID
	applyCreatedNodeAgent(agent, agentRow)
	return nil
}

// RotateCredential atomically replaces only the verifier for an existing
// native agent. Its identity, epoch, streams, counters and convergence state
// remain untouched; the previous credential stops authenticating as soon as
// this transaction commits.
func (r *nativeAgentProvisioningRepo) RotateCredential(ctx context.Context, panelID int64, credentialSHA256 string) (*domain.NodeAgent, error) {
	credentialSHA256 = strings.ToLower(credentialSHA256)
	if len(credentialSHA256) != sha256.Size*2 {
		return nil, fmt.Errorf("%w: credential must be a SHA-256 hex digest", domain.ErrValidation)
	}
	if _, err := hex.DecodeString(credentialSHA256); err != nil {
		return nil, fmt.Errorf("%w: credential must be a SHA-256 hex digest", domain.ErrValidation)
	}

	var updated nodeAgentRow
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var panel xuiPanelRow
		if err := tx.First(&panel, panelID).Error; err != nil {
			return wrapNotFound(err)
		}
		if domain.NormalizePanelKind(domain.PanelKind(panel.Kind)) != domain.PanelKindPSP {
			return fmt.Errorf("%w: panel is not a native node", domain.ErrValidation)
		}
		if err := tx.Where("panel_id = ?", panelID).First(&updated).Error; err != nil {
			return fmt.Errorf("%w: native panel has no agent identity", domain.ErrValidation)
		}
		result := tx.Model(&nodeAgentRow{}).Where("id = ?", updated.ID).
			Update("credential_sha256", credentialSHA256)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return domain.ErrNotFound
		}
		return tx.First(&updated, updated.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return rowToNodeAgent(&updated), nil
}

// DeleteConverged retires both identities in one transaction. A native panel
// may be removed only after its desired config and roster are empty and the
// agent acknowledged those exact bytes; otherwise deleting its credential
// could strand a still-serving core outside control-plane reach.
func (r *nativeAgentProvisioningRepo) DeleteConverged(ctx context.Context, panelID int64) error {
	return runTransactionWithRetry(ctx, r.db, func(tx *gorm.DB) error {
		var panel xuiPanelRow
		if err := tx.First(&panel, panelID).Error; err != nil {
			return wrapNotFound(err)
		}
		if domain.NormalizePanelKind(domain.PanelKind(panel.Kind)) != domain.PanelKindPSP {
			return fmt.Errorf("%w: panel is not a native node", domain.ErrValidation)
		}
		// Resolve through panel_id without taking an InnoDB secondary-index lock,
		// then acquire the canonical agent_id owner lock used by task creation,
		// offering, completion, and applied-state ingestion. Locking this same row
		// through different unique indexes can otherwise deadlock when deletion
		// later needs every secondary index entry.
		var resolved nodeAgentRow
		if err := tx.Where("panel_id = ?", panelID).First(&resolved).Error; err != nil {
			return fmt.Errorf("%w: native panel has no agent identity", domain.ErrValidation)
		}
		locked, err := lockNodeAgentByAgentID(tx, resolved.AgentID)
		if err != nil {
			return err
		}
		if locked.ID != resolved.ID || locked.PanelID != panelID {
			return fmt.Errorf("%w: native panel agent identity changed concurrently", domain.ErrConflict)
		}
		agent := *locked
		var activeTasks []nodeAgentTaskRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("task_id").
			Where("agent_id = ? AND status IN ?", agent.AgentID,
				[]string{string(domain.NodeAgentTaskQueued), string(domain.NodeAgentTaskOffered)}).
			Limit(1).Find(&activeTasks).Error; err != nil {
			return err
		}
		if len(activeTasks) != 0 {
			return fmt.Errorf("%w: native agent still has active task(s)", domain.ErrConflict)
		}
		var clientRefs int64
		if err := tx.Model(&pspClientRow{}).Where("panel_id = ?", panelID).Count(&clientRefs).Error; err != nil {
			return err
		}
		if clientRefs != 0 {
			return fmt.Errorf("%w: native panel still has %d client(s)", domain.ErrValidation, clientRefs)
		}
		var nodeRefs int64
		if err := tx.Model(&nodeRow{}).Where("panel_id = ?", panelID).Count(&nodeRefs).Error; err != nil {
			return err
		}
		if nodeRefs != 0 {
			return fmt.Errorf("%w: panel still has %d node(s); remove or reassign them first", domain.ErrValidation, nodeRefs)
		}
		if err := requireEmptyConvergedNativeStreams(tx, agent.AgentID); err != nil {
			return err
		}
		// Native panels can only own the current psp_clients records checked
		// above. Do not route this transactional deletion through xuiPanelRepo:
		// its transitional legacy-ownership probe intentionally tolerates a
		// missing user_xui_clients table, but PostgreSQL aborts the transaction
		// as soon as that probe touches the retired table.
		if err := tx.Delete(&xuiPanelRow{}, panelID).Error; err != nil {
			return err
		}
		if err := tx.Where("agent_id = ?", agent.AgentID).Delete(&nodeAgentIssueRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("agent_id = ?", agent.AgentID).Delete(&nodeAgentTaskRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("agent_id = ?", agent.AgentID).Delete(&nodeAgentStreamRow{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&nodeAgentRow{}, agent.ID).Error; err != nil {
			return err
		}
		return nil
	})
}

func requireEmptyConvergedNativeStreams(tx *gorm.DB, agentID string) error {
	var streams []nodeAgentStreamRow
	if err := tx.Where("agent_id = ? AND stream IN ?", agentID,
		[]string{string(domain.NodeAgentStreamConfig), string(domain.NodeAgentStreamRoster)}).
		Find(&streams).Error; err != nil {
		return err
	}
	if len(streams) != 2 {
		return fmt.Errorf("%w: native agent stream state is incomplete", domain.ErrValidation)
	}
	for _, stream := range streams {
		if stream.DesiredETag == "" {
			continue // Never delivered; the node could not have served this stream.
		}
		if stream.DesiredETag != stream.AppliedETag {
			return fmt.Errorf("%w: native %s stream has not converged", domain.ErrValidation, stream.Stream)
		}
		switch domain.NodeAgentStreamName(stream.Stream) {
		case domain.NodeAgentStreamConfig:
			var body nodeprotocol.ConfigBody
			if err := json.Unmarshal(stream.DesiredBody, &body); err != nil || len(body.Listeners) != 0 {
				return fmt.Errorf("%w: native config must converge empty before deletion", domain.ErrValidation)
			}
		case domain.NodeAgentStreamRoster:
			var body nodeprotocol.RosterBody
			if err := json.Unmarshal(stream.DesiredBody, &body); err != nil || len(body.Clients) != 0 {
				return fmt.Errorf("%w: native roster must converge empty before deletion", domain.ErrValidation)
			}
		}
	}
	return nil
}
