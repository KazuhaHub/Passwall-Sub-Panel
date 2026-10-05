package sqlstore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MintConfigWithPolicyCandidate serializes on the native-agent owner row, then
// commits the config stream and exact candidate together. Source-only changes
// update candidate metadata even when the wire ETag is unchanged. The return
// value's DesiredBody is omitted on the unchanged path; callers already hold
// that canonical body and only need stream metadata to build the response.
func (r *nodeAgentRepo) MintConfigWithPolicyCandidate(ctx context.Context, agentID string, canonical []byte, meta domain.DestPolicyMint, now time.Time) (*domain.NodeAgentStream, bool, error) {
	if agentID == "" || len(canonical) == 0 || int64(len(canonical)) > protocol.MaxSyncBodyBytes {
		return nil, false, fmt.Errorf("%w: destination candidate config", domain.ErrValidation)
	}
	proof, err := r.policyConfig(canonical)
	if err != nil {
		return nil, false, err
	}
	if !proof.validMint(meta) {
		return nil, false, fmt.Errorf("%w: destination candidate metadata", domain.ErrValidation)
	}
	now, err = destWriteTime(now)
	if err != nil {
		return nil, false, err
	}
	policyBody := proof.policyBody
	body := append([]byte(nil), canonical...)
	etag, digest := proof.etag, proof.digest
	for attempt := 0; attempt < 8; attempt++ {
		var minted *domain.NodeAgentStream
		changed := false
		err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if _, err := lockNodeAgentByAgentID(tx, agentID); err != nil {
				return err
			}
			var current nodeAgentStreamRow
			if err := tx.Omit("DesiredBody").Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ? AND stream = ?", agentID, domain.NodeAgentStreamConfig).First(&current).Error; err != nil {
				return wrapNotFound(err)
			}
			if current.DesiredETag != etag {
				if current.DesiredVersion == math.MaxUint64 {
					return errors.New("mint node agent stream: version exhausted; rebuild epoch")
				}
				result := tx.Model(&nodeAgentStreamRow{}).Where("id = ? AND desired_version = ? AND desired_etag = ?", current.ID, current.DesiredVersion, current.DesiredETag).UpdateColumns(map[string]any{"desired_version": current.DesiredVersion + 1, "desired_etag": etag, "desired_body": body, "pending_since": now, "updated_at": now})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return domain.ErrConflict
				}
				current.DesiredVersion++
				current.DesiredETag = etag
				current.DesiredBody = body
				current.PendingSince = &now
				current.UpdatedAt = now
				changed = true
			}
			var candidate destAgentPolicyRow
			err := tx.Select("agent_id", "desired_sha256", "minted_sha256", "minted_kind", "minted_generation", "minted_context", "minted_at", "collect_effective").Where("agent_id = ?", agentID).First(&candidate).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				candidate = destAgentPolicyRow{AgentID: agentID, DesiredSHA256: meta.DesiredSHA256, MintedSHA256: digest, MintedBody: append(destBytes(nil), policyBody...), MintedKind: string(meta.Kind), MintedGeneration: meta.Generation, MintedContext: meta.Context, MintedAt: &now, CollectEffective: meta.CollectEffective, UpdatedAt: now}
				if err := tx.Create(&candidate).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else if candidate.MintedAt == nil || candidate.DesiredSHA256 != meta.DesiredSHA256 || candidate.MintedSHA256 != digest || candidate.MintedKind != string(meta.Kind) || candidate.MintedGeneration != meta.Generation || candidate.MintedContext != meta.Context || candidate.CollectEffective != meta.CollectEffective {
				if err := tx.Model(&destAgentPolicyRow{}).Where("agent_id = ?", agentID).UpdateColumns(map[string]any{"desired_sha256": meta.DesiredSHA256, "minted_sha256": digest, "minted_body": append([]byte(nil), policyBody...), "minted_kind": string(meta.Kind), "minted_generation": meta.Generation, "minted_context": meta.Context, "minted_at": now, "collect_effective": meta.CollectEffective, "updated_at": now}).Error; err != nil {
					return err
				}
			}
			minted = streamRowToDomain(&current)
			return nil
		})
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		return minted, changed, nil
	}
	return nil, false, fmt.Errorf("%w: destination candidate mint retry limit", domain.ErrConflict)
}

func (p policyConfigProof) validMint(meta domain.DestPolicyMint) bool {
	if meta.Generation < 0 || !policyDigestValid(meta.Context, false) || !policyDigestValid(meta.DesiredSHA256, true) {
		return false
	}
	if meta.CollectEffective != p.collect {
		return false
	}
	switch meta.Kind {
	case domain.DestCandidateDesired:
		return p.active && meta.Generation > 0 && meta.DesiredSHA256 == p.digest
	case domain.DestCandidateFallback:
		return p.active && meta.Generation > 0
	case domain.DestCandidateEmpty, domain.DestCandidatePaused:
		return !p.active && p.emptyAllowed
	default:
		return false
	}
}

var _ ports.NodePolicyCandidateRepo = (*nodeAgentRepo)(nil)

func policyDigestValid(value string, empty bool) bool {
	if value == "" {
		return empty
	}
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
