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
	"gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type nativeAgentProvisioningRepo struct{ db *gorm.DB }

func (r *nativeAgentProvisioningRepo) Create(ctx context.Context, panel *domain.XUIPanel, agent *domain.NodeAgent) error {
	return r.create(ctx, panel, agent, nil)
}

func (r *nativeAgentProvisioningRepo) CreateWithCredential(ctx context.Context, panel *domain.XUIPanel, agent *domain.NodeAgent, raw string) error {
	digest, err := nativeCredentialDigest(raw)
	if err != nil {
		return err
	}
	if agent == nil || strings.ToLower(agent.CredentialSHA256) != digest {
		return fmt.Errorf("%w: native credential does not match its verifier", domain.ErrConflict)
	}
	ciphertext, err := encryptNativeCredential(raw)
	if err != nil {
		return err
	}
	return safeNativeCredentialStorageError(r.create(ctx, panel, agent, &ciphertext))
}

func (r *nativeAgentProvisioningRepo) create(ctx context.Context, panel *domain.XUIPanel, agent *domain.NodeAgent, ciphertext *string) error {
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
		// Never interpolate encrypted credentials into SQL error/slow logs.
		tx = tx.Session(&gorm.Session{Logger: logger.Discard})
		if err := tx.Create(panelRow).Error; err != nil {
			return err
		}
		copyAgent.PanelID = panelRow.ID
		agentRow, err = createNodeAgentRows(tx, &copyAgent)
		if err != nil || ciphertext == nil {
			return err
		}
		result := tx.Model(&nodeAgentRow{}).Where("id = ?", agentRow.ID).
			Update("credential_ciphertext", *ciphertext)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: new native credential recovery copy was not persisted", domain.ErrConflict)
		}
		return nil
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

	return r.rotateCredential(ctx, panelID, credentialSHA256, nil)
}

func (r *nativeAgentProvisioningRepo) RotateCredentialWithSecret(ctx context.Context, panelID int64, raw string) (*domain.NodeAgent, error) {
	digest, err := nativeCredentialDigest(raw)
	if err != nil {
		return nil, err
	}
	ciphertext, err := encryptNativeCredential(raw)
	if err != nil {
		return nil, err
	}
	return r.rotateCredential(ctx, panelID, digest, &ciphertext)
}

func (r *nativeAgentProvisioningRepo) rotateCredential(ctx context.Context, panelID int64, digest string, ciphertext *string) (*domain.NodeAgent, error) {
	var updated *nodeAgentRow
	err := runTransactionWithRetry(ctx, r.db, func(tx *gorm.DB) error {
		tx = tx.Session(&gorm.Session{Logger: logger.Discard})
		var err error
		updated, err = lockNativeCredentialOwner(tx, panelID)
		if err != nil {
			return err
		}
		// Both values change together, including clearing the recovery copy for
		// digest-only rotation. Do not let StoreCredential resurrect an old raw
		// value after a concurrent rotation has replaced its verifier.
		result := tx.Model(&nodeAgentRow{}).Where("id = ? AND credential_sha256 = ?", updated.ID, updated.CredentialSHA256).
			Updates(map[string]any{"credential_sha256": digest, "credential_ciphertext": ciphertext})
		if result.Error != nil {
			return result.Error
		}
		// MySQL reports zero for a valid no-op without CLIENT_FOUND_ROWS. The
		// locked owner's existence was already proven; read back exact identity
		// instead of treating changed-row count as an insertion/existence oracle.
		if err := tx.Omit("CredentialCiphertext").First(updated, updated.ID).Error; err != nil {
			return err
		}
		if updated.CredentialSHA256 != digest {
			return fmt.Errorf("%w: native verifier changed concurrently", domain.ErrConflict)
		}
		return nil
	})
	if err != nil {
		return nil, safeNativeCredentialStorageError(err)
	}
	return rowToNodeAgent(updated), nil
}

func (r *nativeAgentProvisioningRepo) StoreCredential(ctx context.Context, panelID int64, raw string) error {
	digest, err := nativeCredentialDigest(raw)
	if err != nil {
		return err
	}
	ciphertext, err := encryptNativeCredential(raw)
	if err != nil {
		return err
	}
	err = runTransactionWithRetry(ctx, r.db, func(tx *gorm.DB) error {
		tx = tx.Session(&gorm.Session{Logger: logger.Discard})
		owner, err := lockNativeCredentialOwner(tx, panelID)
		if err != nil {
			return err
		}
		if owner.CredentialSHA256 != digest {
			return fmt.Errorf("%w: native credential does not match the current verifier", domain.ErrConflict)
		}
		result := tx.Model(&nodeAgentRow{}).Where("id = ? AND credential_sha256 = ?", owner.ID, digest).
			Update("credential_ciphertext", ciphertext)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: native verifier changed concurrently", domain.ErrConflict)
		}
		return nil
	})
	return safeNativeCredentialStorageError(err)
}

func (r *nativeAgentProvisioningRepo) GetCredential(ctx context.Context, panelID int64) (string, error) {
	var raw string
	err := runTransactionWithRetry(ctx, r.db, func(tx *gorm.DB) error {
		raw = ""
		tx = tx.Session(&gorm.Session{Logger: logger.Discard})
		owner, err := lockNativeCredentialOwner(tx, panelID)
		if err != nil {
			return err
		}
		var secret struct{ CredentialCiphertext *string }
		// The preliminary panel/owner lookup may have established an older
		// MySQL RR snapshot. Read the recovery copy using a current locking
		// read after the owner lock, not that older snapshot's credential pair.
		if err := tx.Model(&nodeAgentRow{}).Select("credential_ciphertext").Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", owner.ID).Take(&secret).Error; err != nil {
			return err
		}
		if secret.CredentialCiphertext == nil {
			return fmt.Errorf("%w: native credential recovery copy is unavailable", domain.ErrNotFound)
		}
		raw, err = decryptNativeCredential(*secret.CredentialCiphertext)
		if err != nil {
			return err
		}
		digest, err := nativeCredentialDigest(raw)
		if err != nil || digest != owner.CredentialSHA256 {
			raw = ""
			return errors.New("native credential recovery copy failed integrity validation")
		}
		return nil
	})
	if err != nil {
		return "", safeNativeCredentialStorageError(err)
	}
	return raw, nil
}

func nativeCredentialDigest(raw string) (string, error) {
	if len(raw) < nodeprotocol.MinNodeCredentialBytes || len(raw) > nodeprotocol.MaxNodeCredentialBytes {
		return "", fmt.Errorf("%w: native credential exceeds allowed size bounds", domain.ErrValidation)
	}
	for i := range raw {
		if raw[i] < 0x21 || raw[i] > 0x7e {
			return "", fmt.Errorf("%w: native credential must contain canonical printable ASCII", domain.ErrValidation)
		}
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:]), nil
}

func lockNativeCredentialOwner(tx *gorm.DB, panelID int64) (*nodeAgentRow, error) {
	if panelID <= 0 {
		return nil, fmt.Errorf("%w: native panel ID must be positive", domain.ErrValidation)
	}
	var panel xuiPanelRow
	if err := tx.Select("id, kind").First(&panel, panelID).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	if domain.NormalizePanelKind(domain.PanelKind(panel.Kind)) != domain.PanelKindPSP {
		return nil, fmt.Errorf("%w: panel is not a native node", domain.ErrValidation)
	}
	var resolved nodeAgentRow
	if err := tx.Select("id, agent_id, panel_id").Where("panel_id = ?", panelID).First(&resolved).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	owner, err := lockNodeAgentByAgentID(tx, resolved.AgentID)
	if err != nil {
		return nil, err
	}
	if owner.ID != resolved.ID || owner.PanelID != panelID {
		return nil, fmt.Errorf("%w: native agent identity changed concurrently", domain.ErrConflict)
	}
	return owner, nil
}

type nativeCredentialStorageError struct{ cause error }

func (*nativeCredentialStorageError) Error() string {
	return "native credential storage operation failed"
}
func (e *nativeCredentialStorageError) Unwrap() error { return e.cause }

func safeNativeCredentialStorageError(err error) error {
	if err == nil || errors.Is(err, domain.ErrValidation) || errors.Is(err, domain.ErrConflict) ||
		errors.Is(err, domain.ErrNotFound) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &nativeCredentialStorageError{cause: err}
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
		if err := tx.Select("id, agent_id, panel_id").Where("panel_id = ?", panelID).First(&resolved).Error; err != nil {
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
		// Earlier plain identity lookups can establish an old MySQL RR snapshot.
		// This must be a current locking read after the owner lock, so a receipt
		// committed by the previous owner-lock holder cannot be overlooked.
		var evidence []nodeAgentTaskResultQuarantineRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("agent_id, task_id").
			Where("agent_id = ?", agent.AgentID).Limit(1).Find(&evidence).Error; err != nil {
			return err
		}
		if len(evidence) != 0 {
			return fmt.Errorf("%w: native agent still has quarantined result evidence", domain.ErrConflict)
		}
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
