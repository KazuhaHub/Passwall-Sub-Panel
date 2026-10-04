package sqlstore

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Content is capped by the services, but MySQL's default BLOB is only 64 KiB.
// Use longblob explicitly so source text, expanded lists and snapshots retain
// all bytes on every supported database.
type destBytes []byte

func (destBytes) GormDataType() string { return "bytes" }
func (destBytes) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	switch db.Dialector.Name() {
	case "mysql":
		return "longblob"
	case "postgres":
		return "bytea"
	default:
		return "blob"
	}
}
func (b destBytes) Value() (driver.Value, error) {
	if b == nil {
		return nil, nil
	}
	return []byte(b), nil
}
func (b *destBytes) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*b = nil
	case []byte:
		*b = append(destBytes{}, v...)
	case string:
		*b = destBytes(v)
	default:
		return fmt.Errorf("unsupported destination bytes scan: %T", value)
	}
	return nil
}

type jsonDestInline domain.DestInline

func (v jsonDestInline) Value() (driver.Value, error) {
	b, err := json.Marshal(domain.DestInline(v))
	return string(b), err
}
func (jsonDestInline) GormDataType() string                          { return "text" }
func (jsonDestInline) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }
func (v *jsonDestInline) Scan(value any) error {
	*v = jsonDestInline{}
	if value == nil {
		return nil
	}
	var b []byte
	switch raw := value.(type) {
	case []byte:
		b = raw
	case string:
		b = []byte(raw)
	default:
		return fmt.Errorf("unsupported destination match scan: %T", value)
	}
	return json.Unmarshal(b, (*domain.DestInline)(v))
}

type destListRow struct {
	ID                      int64  `gorm:"primaryKey;autoIncrement"`
	Name                    string `gorm:"size:128"`
	Kind                    string `gorm:"size:16"`
	SourceURL               string `gorm:"size:1024"`
	GeositeCategory         string `gorm:"size:128"`
	GeositeAttrs            string `gorm:"size:128"`
	Entries, SourceText     destBytes
	EntryCount, RegexpCount int
	ContentSHA256           string `gorm:"size:64"`
	LastFetchedAt           *time.Time
	LastError               string `gorm:"size:512"`
	OwnerGroupID            int64  `gorm:"not null;default:0"`
	CreatedAt, UpdatedAt    time.Time
}

func (destListRow) TableName() string { return "dest_lists" }

type destPolicyRow struct {
	ID                    int64  `gorm:"primaryKey;autoIncrement"`
	Name                  string `gorm:"size:128;uniqueIndex;not null"`
	Action                string `gorm:"size:16"`
	ListIDs               jsonInt64s
	Inline                jsonDestInline
	Scope                 string `gorm:"size:16"`
	GroupIDs              jsonInt64s
	Priority              int
	Enabled, CountsAsRisk bool
	TemplateKey           string `gorm:"size:32;not null;default:''"`
	CreatedAt, UpdatedAt  time.Time
}

func (destPolicyRow) TableName() string { return "dest_policies" }

type destExemptionRow struct {
	UserID    int64  `gorm:"primaryKey;autoIncrement:false"`
	Reason    string `gorm:"size:255"`
	CreatedBy int64
	CreatedAt time.Time
	ExpiresAt *time.Time
}

func (destExemptionRow) TableName() string { return "dest_exemptions" }

type destGroupModeRow struct {
	GroupID                 int64  `gorm:"primaryKey;autoIncrement:false"`
	Mode, Stage             string `gorm:"size:16"`
	ListIDs                 jsonInt64s
	BaseListID, ExtraListID int64
	StageChangedAt          *time.Time
	UpdatedAt               time.Time
}

func (destGroupModeRow) TableName() string { return "dest_group_modes" }

type destPolicyStateRow struct {
	ID                                           int64 `gorm:"primaryKey;autoIncrement:false"`
	Generation, PublishedGeneration              int64
	FirstUnpublishedAt, LastWriteAt, PublishedAt *time.Time
	Paused                                       bool
	PublishError                                 *string `gorm:"type:text"`
	PublishErrorAt                               *time.Time
}

func (destPolicyStateRow) TableName() string { return "dest_policy_state" }

type destPolicySnapshotRow struct {
	Generation int64 `gorm:"primaryKey;autoIncrement:false"`
	Body       destBytes
	CreatedAt  time.Time
}

func (destPolicySnapshotRow) TableName() string { return "dest_policy_snapshots" }

type destAgentPolicyRow struct {
	AgentID                     string `gorm:"primaryKey;size:64"`
	DesiredSHA256, MintedSHA256 string `gorm:"size:64"`
	MintedBody                  destBytes
	MintedKind                  string `gorm:"size:16"`
	MintedGeneration            int64
	MintedContext               string `gorm:"size:64"`
	MintedAt                    *time.Time
	FallbackReason              string `gorm:"size:32;not null;default:''"`
	RejectedGeneration          int64
	FallbackExhausted           bool    `gorm:"not null;default:false"`
	OverLimit                   *string `gorm:"type:text"`
	PrecheckListeners           jsonStrings
	AppliedSHA256               string `gorm:"size:64"`
	AppliedBody                 destBytes
	AppliedAt                   *time.Time
	AppliedRuleCount            int
	AppliedGroups               jsonInt64s
	CollectEffective            string `gorm:"size:16;not null;default:''"`
	ReportedSHA256              string `gorm:"size:64"`
	ReportedState               string `gorm:"size:16"`
	ReportedIssue               string `gorm:"size:64"`
	ReportedListeners           jsonStrings
	ReportedAt                  *time.Time
	UpdatedAt                   time.Time
}

func (destAgentPolicyRow) TableName() string { return "dest_agent_policy" }

type destHitRow struct {
	HourMS          int64  `gorm:"primaryKey;autoIncrement:false;index:idx_dest_hits_user_hour,priority:2;index:idx_dest_hits_source_hour,priority:2;index:idx_dest_hits_panel_hour,priority:2"`
	PanelID         int64  `gorm:"primaryKey;autoIncrement:false;index:idx_dest_hits_panel_hour,priority:1"`
	UserID          int64  `gorm:"primaryKey;autoIncrement:false;index:idx_dest_hits_user_hour,priority:1"`
	Source          string `gorm:"primaryKey;size:24;index:idx_dest_hits_source_hour,priority:1"`
	Action          string `gorm:"primaryKey;size:8"`
	Dest            string `gorm:"primaryKey;size:253"`
	Port            int    `gorm:"primaryKey;autoIncrement:false"`
	Count           int64
	FirstAt, LastAt time.Time
}

func (destHitRow) TableName() string { return "dest_hits" }

type destUsageHourlyRow struct {
	HourMS  int64  `gorm:"primaryKey;autoIncrement:false"`
	PanelID int64  `gorm:"primaryKey;autoIncrement:false"`
	UserID  int64  `gorm:"primaryKey;autoIncrement:false"`
	Site    string `gorm:"primaryKey;size:253"`
	Count   int64
}

func (destUsageHourlyRow) TableName() string { return "dest_usage_hourly" }

type destAuditBatchRow struct {
	AgentID    string `gorm:"primaryKey;size:64"`
	BatchID    string `gorm:"primaryKey;size:32"`
	Kind       string `gorm:"size:16"`
	HourMS     int64
	ReceivedAt time.Time
}

func (destAuditBatchRow) TableName() string { return "dest_audit_batches" }

type destAuditLossHourlyRow struct {
	ObservedHourMS          int64  `gorm:"primaryKey;autoIncrement:false"`
	PanelID                 int64  `gorm:"primaryKey;autoIncrement:false"`
	Kind                    string `gorm:"primaryKey;size:16"`
	Reason                  string `gorm:"primaryKey;size:32"`
	Rows, Events, Unmatched int64
}

func (destAuditLossHourlyRow) TableName() string { return "dest_audit_loss_hourly" }

type destAuditIngestBudgetRow struct {
	AgentID        string `gorm:"primaryKey;size:64"`
	ReceivedHourMS int64  `gorm:"primaryKey;autoIncrement:false"`
	Kind           string `gorm:"primaryKey;size:16"`
	RowsReserved   int64
}

func (destAuditIngestBudgetRow) TableName() string { return "dest_audit_ingest_budget" }
