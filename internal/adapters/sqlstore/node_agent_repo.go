package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nodeAgentRow struct {
	ID                     int64  `gorm:"primaryKey;autoIncrement"`
	AgentID                string `gorm:"size:64;not null;uniqueIndex"`
	PanelID                int64  `gorm:"not null;uniqueIndex"`
	Epoch                  uint64 `gorm:"not null;default:1"`
	CredentialSHA256       string `gorm:"size:64;not null;uniqueIndex"`
	DesiredCoreVersion     string `gorm:"size:32;not null;default:''"`
	AllowRestrictedReality bool   `gorm:"not null;default:false"`
	LastSeen               *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (nodeAgentRow) TableName() string { return "node_agents" }

type nodeAgentStreamRow struct {
	ID      int64  `gorm:"primaryKey;autoIncrement"`
	AgentID string `gorm:"size:64;not null;uniqueIndex:uk_node_agent_stream,priority:1"`
	Stream  string `gorm:"size:16;not null;uniqueIndex:uk_node_agent_stream,priority:2"`

	DesiredVersion uint64 `gorm:"not null;default:0"`
	DesiredETag    string `gorm:"column:desired_etag;size:64;not null;default:''"`
	DesiredBody    []byte

	AppliedVersion uint64 `gorm:"not null;default:0"`
	AppliedEpoch   uint64 `gorm:"not null;default:0"`
	AppliedETag    string `gorm:"column:applied_etag;size:64;not null;default:''"`
	PendingSince   *time.Time
	LastSeen       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (nodeAgentStreamRow) TableName() string { return "node_agent_streams" }

type nodeAgentRepo struct{ db *gorm.DB }

func (r *nodeAgentRepo) Create(ctx context.Context, agent *domain.NodeAgent) error {
	if err := validateNewNodeAgent(agent); err != nil {
		return err
	}
	copy := *agent
	var row *nodeAgentRow
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		row, err = createNodeAgentRows(tx, &copy)
		return err
	})
	if err != nil {
		return err
	}
	applyCreatedNodeAgent(agent, row)
	return nil
}

func validateNewNodeAgent(agent *domain.NodeAgent) error {
	if agent == nil || agent.AgentID == "" || agent.PanelID == 0 {
		return errors.New("create node agent: agent ID and panel ID required")
	}
	if len(agent.CredentialSHA256) != sha256.Size*2 {
		return errors.New("create node agent: credential must be a SHA-256 hex digest")
	}
	if _, err := hex.DecodeString(agent.CredentialSHA256); err != nil {
		return errors.New("create node agent: credential must be a SHA-256 hex digest")
	}
	agent.CredentialSHA256 = strings.ToLower(agent.CredentialSHA256)
	return nil
}

func createNodeAgentRows(tx *gorm.DB, agent *domain.NodeAgent) (*nodeAgentRow, error) {
	epoch := agent.Epoch
	if epoch == 0 {
		epoch = 1
	}
	row := &nodeAgentRow{
		AgentID: agent.AgentID, PanelID: agent.PanelID, Epoch: epoch,
		CredentialSHA256:       agent.CredentialSHA256,
		DesiredCoreVersion:     agent.DesiredCoreVersion,
		AllowRestrictedReality: agent.AllowRestrictedReality,
		LastSeen:               agent.LastSeen,
	}
	if err := tx.Create(row).Error; err != nil {
		return nil, err
	}
	for _, stream := range []domain.NodeAgentStreamName{
		domain.NodeAgentStreamConfig,
		domain.NodeAgentStreamRoster,
		domain.NodeAgentStreamDirectives,
	} {
		if err := tx.Create(&nodeAgentStreamRow{AgentID: agent.AgentID, Stream: string(stream)}).Error; err != nil {
			return nil, err
		}
	}
	return row, nil
}

func applyCreatedNodeAgent(agent *domain.NodeAgent, row *nodeAgentRow) {
	agent.ID = row.ID
	agent.Epoch = row.Epoch
	agent.CredentialSHA256 = row.CredentialSHA256
	agent.DesiredCoreVersion = row.DesiredCoreVersion
	agent.AllowRestrictedReality = row.AllowRestrictedReality
	agent.CreatedAt = row.CreatedAt
	agent.UpdatedAt = row.UpdatedAt
}

func rowToNodeAgent(row *nodeAgentRow) *domain.NodeAgent {
	return &domain.NodeAgent{
		ID: row.ID, AgentID: row.AgentID, PanelID: row.PanelID, Epoch: row.Epoch,
		CredentialSHA256:       row.CredentialSHA256,
		DesiredCoreVersion:     row.DesiredCoreVersion,
		AllowRestrictedReality: row.AllowRestrictedReality,
		LastSeen:               row.LastSeen,
		CreatedAt:              row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func (r *nodeAgentRepo) List(ctx context.Context) ([]*domain.NodeAgent, error) {
	var rows []nodeAgentRow
	if err := r.db.WithContext(ctx).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.NodeAgent, len(rows))
	for i := range rows {
		out[i] = rowToNodeAgent(&rows[i])
	}
	return out, nil
}

func streamRowToDomain(row *nodeAgentStreamRow) *domain.NodeAgentStream {
	body := append([]byte(nil), row.DesiredBody...)
	return &domain.NodeAgentStream{
		AgentID: row.AgentID, Stream: domain.NodeAgentStreamName(row.Stream),
		DesiredVersion: row.DesiredVersion, DesiredETag: row.DesiredETag, DesiredBody: body,
		AppliedVersion: row.AppliedVersion, AppliedEpoch: row.AppliedEpoch, AppliedETag: row.AppliedETag,
		PendingSince: row.PendingSince, LastSeen: row.LastSeen,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func (r *nodeAgentRepo) GetByAgentID(ctx context.Context, agentID string) (*domain.NodeAgent, error) {
	var row nodeAgentRow
	if err := r.db.WithContext(ctx).Where("agent_id = ?", agentID).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return rowToNodeAgent(&row), nil
}

func (r *nodeAgentRepo) GetByCredentialSHA256(ctx context.Context, digest string) (*domain.NodeAgent, error) {
	if len(digest) != sha256.Size*2 {
		return nil, domain.ErrNotFound
	}
	var row nodeAgentRow
	if err := r.db.WithContext(ctx).Where("credential_sha256 = ?", strings.ToLower(digest)).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return rowToNodeAgent(&row), nil
}

func (r *nodeAgentRepo) GetByPanelID(ctx context.Context, panelID int64) (*domain.NodeAgent, error) {
	var row nodeAgentRow
	if err := r.db.WithContext(ctx).Where("panel_id = ?", panelID).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return rowToNodeAgent(&row), nil
}

func (r *nodeAgentRepo) UpdateCoreSelection(ctx context.Context, agentID, version string, allowRestrictedReality bool) error {
	if agentID == "" || version == "" {
		return errors.New("update node agent core selection: agent ID and version required")
	}
	result := r.db.WithContext(ctx).Model(&nodeAgentRow{}).Where("agent_id = ?", agentID).Updates(map[string]any{
		"desired_core_version":     version,
		"allow_restricted_reality": allowRestrictedReality,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *nodeAgentRepo) TouchLastSeen(ctx context.Context, agentID string, seenAt time.Time) error {
	return r.db.WithContext(ctx).Model(&nodeAgentRow{}).Where("agent_id = ?", agentID).
		Update("last_seen", seenAt.UTC()).Error
}

func (r *nodeAgentRepo) GetStream(ctx context.Context, agentID string, stream domain.NodeAgentStreamName) (*domain.NodeAgentStream, error) {
	if !stream.Valid() {
		return nil, fmt.Errorf("get node agent stream: invalid stream %q", stream)
	}
	var row nodeAgentStreamRow
	if err := r.db.WithContext(ctx).Where("agent_id = ? AND stream = ?", agentID, stream).
		First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return streamRowToDomain(&row), nil
}

func (r *nodeAgentRepo) ListStreams(ctx context.Context, agentID string) ([]*domain.NodeAgentStream, error) {
	var rows []nodeAgentStreamRow
	if err := r.db.WithContext(ctx).Where("agent_id = ?", agentID).Order("stream").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.NodeAgentStream, len(rows))
	for i := range rows {
		out[i] = streamRowToDomain(&rows[i])
	}
	return out, nil
}

func contentETag(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// MintStream implements an optimistic compare-and-swap. The content check is
// deliberately before the version increment; retrying identical input is a
// read-only operation even when another writer raced this one.
func (r *nodeAgentRepo) MintStream(ctx context.Context, agentID string, stream domain.NodeAgentStreamName, canonicalBody []byte, now time.Time) (*domain.NodeAgentStream, bool, error) {
	if agentID == "" || !stream.Valid() || len(canonicalBody) == 0 {
		return nil, false, errors.New("mint node agent stream: agent, valid stream and body required")
	}
	body := append([]byte(nil), canonicalBody...)
	etag := contentETag(body)
	now = now.UTC()
	for attempt := 0; attempt < 8; attempt++ {
		var current nodeAgentStreamRow
		if err := r.db.WithContext(ctx).Where("agent_id = ? AND stream = ?", agentID, stream).
			First(&current).Error; err != nil {
			return nil, false, wrapNotFound(err)
		}
		if current.DesiredETag == etag {
			if !bytes.Equal(current.DesiredBody, body) {
				return nil, false, errors.New("mint node agent stream: SHA-256 collision")
			}
			return streamRowToDomain(&current), false, nil
		}
		if current.DesiredVersion == math.MaxUint64 {
			return nil, false, errors.New("mint node agent stream: version exhausted; rebuild epoch")
		}
		nextVersion := current.DesiredVersion + 1
		result := r.db.WithContext(ctx).Model(&nodeAgentStreamRow{}).
			Where("id = ? AND desired_version = ?", current.ID, current.DesiredVersion).
			Updates(map[string]any{
				"desired_version": nextVersion,
				"desired_etag":    etag,
				"desired_body":    body,
				"pending_since":   now,
			})
		if result.Error != nil {
			return nil, false, result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		minted, err := r.GetStream(ctx, agentID, stream)
		return minted, true, err
	}
	return nil, false, errors.New("mint node agent stream: compare-and-swap retry limit exceeded")
}

func (r *nodeAgentRepo) RecordApplied(ctx context.Context, agentID string, stream domain.NodeAgentStreamName, epoch, version uint64, etag string, seenAt time.Time) error {
	if agentID == "" || !stream.Valid() {
		return errors.New("record applied node agent stream: agent and valid stream required")
	}
	seenAt = seenAt.UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var agent nodeAgentRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ?", agentID).First(&agent).Error; err != nil {
			return wrapNotFound(err)
		}
		if err := tx.Model(&nodeAgentRow{}).Where("id = ?", agent.ID).
			Update("last_seen", seenAt).Error; err != nil {
			return err
		}
		var current nodeAgentStreamRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND stream = ?", agentID, stream).First(&current).Error; err != nil {
			return wrapNotFound(err)
		}
		// An agent may legitimately be behind PSP, including on an older epoch
		// after PSP rebuilt its desired documents. It cannot be ahead of the
		// authoritative issuer, though, and one minted version has exactly one
		// content digest. Reject impossible acknowledgements before they can
		// poison convergence state. Older versions keep accepting historical
		// ETags so A/B/A content convergence remains valid.
		if epoch > agent.Epoch {
			return fmt.Errorf("record applied node agent stream: reported epoch %d exceeds desired epoch %d", epoch, agent.Epoch)
		}
		if epoch == agent.Epoch && version > current.DesiredVersion {
			return fmt.Errorf("record applied node agent stream: reported version %d exceeds desired version %d", version, current.DesiredVersion)
		}
		if epoch == agent.Epoch && version == current.DesiredVersion && etag != current.DesiredETag {
			return errors.New("record applied node agent stream: reported etag conflicts with desired version")
		}
		pendingSince := current.PendingSince
		if epoch == agent.Epoch && etag != "" && etag == current.DesiredETag {
			pendingSince = nil
		} else if pendingSince == nil {
			pendingSince = &seenAt
		}
		return tx.Model(&nodeAgentStreamRow{}).Where("id = ?", current.ID).Updates(map[string]any{
			"applied_epoch":   epoch,
			"applied_version": version,
			"applied_etag":    etag,
			"pending_since":   pendingSince,
			"last_seen":       seenAt,
		}).Error
	})
}

var _ ports.NodeAgentRepo = (*nodeAgentRepo)(nil)
