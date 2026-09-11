package sqlstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nodeAgentIssueRow struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	AgentID        string `gorm:"size:64;not null;uniqueIndex:uk_node_agent_issue,priority:1;index"`
	Fingerprint    string `gorm:"size:64;not null;uniqueIndex:uk_node_agent_issue,priority:2"`
	Code           string `gorm:"size:128;not null;index"`
	ObjectKey      string `gorm:"column:object_key;size:512;not null"`
	Detail         string `gorm:"type:text;not null"`
	FirstSeenAt    time.Time
	LastSeenAt     time.Time  `gorm:"index"`
	AcknowledgedAt *time.Time `gorm:"index"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (nodeAgentIssueRow) TableName() string { return "node_agent_issues" }

type nodeAgentIssueRepo struct{ db *gorm.DB }

func nodeIssueFingerprint(code, key, detail string) string {
	payload, _ := json.Marshal([3]string{code, key, detail})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (r *nodeAgentIssueRepo) RecordBatch(ctx context.Context, agentID string, issues []domain.NodeAgentIssue, seenAt time.Time) error {
	if strings.TrimSpace(agentID) == "" {
		return errors.New("record node agent issues: agent ID required")
	}
	if len(issues) == 0 {
		return nil
	}
	seenAt = seenAt.UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, issue := range issues {
			if strings.TrimSpace(issue.Code) == "" {
				return errors.New("record node agent issues: code required")
			}
			row := nodeAgentIssueRow{
				AgentID: agentID, Fingerprint: nodeIssueFingerprint(issue.Code, issue.Key, issue.Detail),
				Code: issue.Code, ObjectKey: issue.Key, Detail: issue.Detail,
				FirstSeenAt: seenAt, LastSeenAt: seenAt,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "agent_id"}, {Name: "fingerprint"}},
				DoUpdates: clause.Assignments(map[string]any{
					"last_seen_at": seenAt,
					"updated_at":   seenAt,
				}),
			}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func nodeAgentIssueToDomain(row *nodeAgentIssueRow) *domain.NodeAgentIssue {
	return &domain.NodeAgentIssue{
		ID: row.ID, AgentID: row.AgentID, Code: row.Code, Key: row.ObjectKey, Detail: row.Detail,
		FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt,
		AcknowledgedAt: row.AcknowledgedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func (r *nodeAgentIssueRepo) GetByID(ctx context.Context, id int64) (*domain.NodeAgentIssue, error) {
	var row nodeAgentIssueRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return nodeAgentIssueToDomain(&row), nil
}

func (r *nodeAgentIssueRepo) List(ctx context.Context, filter ports.NodeAgentIssueFilter) ([]*domain.NodeAgentIssue, int64, error) {
	query := r.db.WithContext(ctx).Model(&nodeAgentIssueRow{})
	if filter.AgentID != "" {
		query = query.Where("agent_id = ?", filter.AgentID)
	}
	if filter.Code != "" {
		query = query.Where("code = ?", filter.Code)
	}
	if filter.Acknowledged != nil {
		if *filter.Acknowledged {
			query = query.Where("acknowledged_at IS NOT NULL")
		} else {
			query = query.Where("acknowledged_at IS NULL")
		}
	}
	if like := keywordLike(filter.Keyword); like != "" {
		query = query.Where(likeCols("agent_id", "code", "object_key", "detail"), like, like, like, like)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	query = query.Order("last_seen_at DESC, id DESC")
	if filter.PageSize > 0 {
		page := filter.Page
		if page < 1 {
			page = 1
		}
		query = query.Offset((page - 1) * filter.PageSize).Limit(filter.PageSize)
	}
	var rows []nodeAgentIssueRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]*domain.NodeAgentIssue, len(rows))
	for i := range rows {
		out[i] = nodeAgentIssueToDomain(&rows[i])
	}
	return out, total, nil
}

func (r *nodeAgentIssueRepo) Acknowledge(ctx context.Context, id int64, acknowledgedAt time.Time) error {
	result := r.db.WithContext(ctx).Model(&nodeAgentIssueRow{}).Where("id = ?", id).
		Update("acknowledged_at", acknowledgedAt.UTC())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

var _ ports.NodeAgentIssueRepo = (*nodeAgentIssueRepo)(nil)
