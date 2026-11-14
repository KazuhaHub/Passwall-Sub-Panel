package sqlstore

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Destination-policy ("access control") tables.
//
// THE REPOS ARE CONCRETE TYPES, NOT ports.Repos FIELDS, like the risk tables:
// each consumer (the list service, the catalog, the policy compiler, the admin
// API, the hourly cleanup) declares the narrow interface it needs, so a new
// table never widens the 29-field aggregate or the three-way override in
// app.go.
//
// LARGE COLUMNS ARE []byte. A list's entries are the expansion of a geosite
// category, megabytes of text; MySQL's TEXT holds 64 KiB. A []byte field maps
// to LONGBLOB on MySQL, BYTEA on PostgreSQL and BLOB on SQLite, and the drivers
// hand all three back as []byte (the same choice node_agent_streams.desired_body
// made). The columns that are NOT large keep this package's convention: JSON in
// text, never a driver-inferred array.
//
// NO text COLUMN HAS A DEFAULT (MySQL error 1101): a column that needs a
// non-null backfill is a sized varchar. TestSchemaNoDefaultOnTextColumns holds
// every model here to it.

// Widths of the bounded columns, checked before a write: left to the database
// an over-long value is stored by SQLite, refused by PostgreSQL and truncated
// or refused by MySQL depending on SQL mode. TestDestRows_WidthsMatchTheColumns
// holds each to its size tag.
const (
	destNameWidth        = 128
	destKindWidth        = 16
	destSideWidth        = 8
	destURLWidth         = 1024
	destCategoryWidth    = 128
	destAttrsWidth       = 128
	destErrorWidth       = 512
	destDescriptionWidth = 512
	destTemplateKeyWidth = 32
	destStateWidth       = 8
	destReasonWidth      = 255
	destAgentIDWidth     = 64
	destShaWidth         = 64
)

// destListRow is one list. Entries and SourceText are nullable bytes: NULL and
// empty both read as "none".
type destListRow struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	Name            string `gorm:"size:128;not null"`
	Kind            string `gorm:"size:16;not null;index:idx_dest_list_geosite,priority:1"`
	Side            string `gorm:"size:8;not null;default:'dest'"`
	SourceURL       string `gorm:"column:source_url;size:1024;not null;default:''"`
	GeositeCategory string `gorm:"column:geosite_category;size:128;not null;default:'';index:idx_dest_list_geosite,priority:2"`
	GeositeAttrs    string `gorm:"column:geosite_attrs;size:128;not null;default:''"`
	Entries         []byte `gorm:"column:entries"`
	SourceText      []byte `gorm:"column:source_text"`
	EntryCount      int    `gorm:"not null;default:0"`
	RegexpCount     int    `gorm:"not null;default:0"`
	ContentSHA256   string `gorm:"column:content_sha256;size:64;not null;default:''"`
	LastFetchedAt   *time.Time
	LastError       string `gorm:"size:512;not null;default:''"`
	// OwnerGroupID is non-zero for a list an allowlist group owns (batch 3).
	OwnerGroupID int64 `gorm:"not null;default:0"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (destListRow) TableName() string { return "dest_lists" }

func (r destListRow) toDomain() *domain.DestList {
	return &domain.DestList{
		ID: r.ID, Name: r.Name, Kind: domain.DestListKind(r.Kind), Side: domain.DestListSide(r.Side),
		SourceURL: r.SourceURL, GeositeCategory: r.GeositeCategory, GeositeAttrs: r.GeositeAttrs,
		Entries: r.Entries, SourceText: r.SourceText, EntryCount: r.EntryCount, RegexpCount: r.RegexpCount,
		ContentSHA256: r.ContentSHA256, LastFetchedAt: utcPtr(r.LastFetchedAt), LastError: r.LastError,
		OwnerGroupID: r.OwnerGroupID, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// destInlineJSON is the policy's built-in match, JSON in a text column.
type destInlineJSON domain.DestPolicyInline

func (j destInlineJSON) Value() (driver.Value, error) {
	b, err := json.Marshal(domain.DestPolicyInline(j))
	return string(b), err
}

// GormDBDataType pins the column to text on every dialect: a struct would
// otherwise be left for the driver to type.
func (destInlineJSON) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }

func (j *destInlineJSON) Scan(value any) error {
	var b []byte
	switch v := value.(type) {
	case nil:
		*j = destInlineJSON{}
		return nil
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("unsupported scan type for destInlineJSON: %T", value)
	}
	if len(b) == 0 {
		*j = destInlineJSON{}
		return nil
	}
	return json.Unmarshal(b, (*domain.DestPolicyInline)(j))
}

type destPolicyRow struct {
	ID           int64          `gorm:"primaryKey;autoIncrement"`
	Name         string         `gorm:"size:128;not null;uniqueIndex:uk_dest_policy_name"`
	Description  string         `gorm:"size:512;not null;default:''"`
	Action       string         `gorm:"size:16;not null;index:idx_dest_policy_order,priority:1"`
	ListIDs      jsonInt64s     `gorm:"column:list_ids;type:text;not null"`
	Inline       destInlineJSON `gorm:"column:inline;type:text;not null"`
	State        string         `gorm:"size:8;not null;default:'off'"`
	Scope        string         `gorm:"size:16;not null;default:'all'"`
	GroupIDs     jsonInt64s     `gorm:"column:group_ids;type:text;not null"`
	PanelIDs     jsonInt64s     `gorm:"column:panel_ids;type:text;not null"`
	Priority     int            `gorm:"not null;default:0;index:idx_dest_policy_order,priority:2"`
	CountsAsRisk bool           `gorm:"not null;default:false"`
	TemplateKey  string         `gorm:"size:32;not null;default:''"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (destPolicyRow) TableName() string { return "dest_policies" }

func (r destPolicyRow) toDomain() *domain.DestPolicy {
	return &domain.DestPolicy{
		ID: r.ID, Name: r.Name, Description: r.Description, Action: domain.DestAction(r.Action),
		State: domain.DestState(r.State), ListIDs: nilIfEmptyInt64s(r.ListIDs), Inline: domain.DestPolicyInline(r.Inline),
		Scope: domain.DestScope(r.Scope), GroupIDs: nilIfEmptyInt64s(r.GroupIDs), PanelIDs: nilIfEmptyInt64s(r.PanelIDs),
		Priority: r.Priority, CountsAsRisk: r.CountsAsRisk, TemplateKey: r.TemplateKey,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

type destExemptionRow struct {
	UserID    int64  `gorm:"primaryKey;autoIncrement:false"`
	Reason    string `gorm:"size:255;not null;default:''"`
	CreatedBy int64  `gorm:"not null;default:0"`
	CreatedAt time.Time
	ExpiresAt *time.Time `gorm:"index:idx_dest_exemption_expires"`
}

func (destExemptionRow) TableName() string { return "dest_exemptions" }

type destSourceRuleRow struct {
	NodeID    int64       `gorm:"primaryKey;autoIncrement:false"`
	Countries jsonStrings `gorm:"column:countries;type:text;not null"`
	CIDRs     jsonStrings `gorm:"column:cidrs;type:text;not null"`
	State     string      `gorm:"size:8;not null;default:'off'"`
	UpdatedAt time.Time
}

func (destSourceRuleRow) TableName() string { return "dest_source_rules" }

func (r destSourceRuleRow) toDomain() *domain.DestSourceRule {
	return &domain.DestSourceRule{
		NodeID: r.NodeID, Countries: nilIfEmptyStrings(r.Countries), CIDRs: nilIfEmptyStrings(r.CIDRs),
		State: domain.DestState(r.State), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// destPolicyStateRow is the single row (id 1) debouncing publication.
type destPolicyStateRow struct {
	ID                  int64 `gorm:"primaryKey;autoIncrement:false"`
	Generation          int64 `gorm:"not null;default:0"`
	PublishedGeneration int64 `gorm:"not null;default:0"`
	FirstUnpublishedAt  *time.Time
	LastWriteAt         *time.Time
	PublishedAt         *time.Time
	// Paused is a real bool with a false default: GORM omitting a false from
	// an INSERT therefore changes nothing.
	Paused         bool    `gorm:"not null;default:false"`
	GeoIPSHA256    string  `gorm:"column:geoip_sha256;size:64;not null;default:''"`
	GeositeRelease string  `gorm:"column:geosite_release;size:64;not null;default:''"`
	PublishError   *string `gorm:"column:publish_error;type:text"`
	PublishErrorAt *time.Time
}

func (destPolicyStateRow) TableName() string { return "dest_policy_state" }

type destPolicySnapshotRow struct {
	Generation int64  `gorm:"primaryKey;autoIncrement:false"`
	Body       []byte `gorm:"column:body"`
	CreatedAt  time.Time
}

func (destPolicySnapshotRow) TableName() string { return "dest_policy_snapshots" }

type destAgentPolicyRow struct {
	AgentID            string `gorm:"primaryKey;size:64;autoIncrement:false"`
	DesiredSHA256      string `gorm:"column:desired_sha256;size:64;not null;default:''"`
	MintedSHA256       string `gorm:"column:minted_sha256;size:64;not null;default:''"`
	MintedAt           *time.Time
	FallbackReason     string  `gorm:"size:32;not null;default:''"`
	RejectedGeneration int64   `gorm:"not null;default:0"`
	OverLimit          *string `gorm:"column:over_limit;type:text"`
	PrecheckListeners  *string `gorm:"column:precheck_listeners;type:text"`
	ReportOnlyShed     string  `gorm:"column:report_only_shed;size:16;not null;default:''"`
	AppliedSHA256      string  `gorm:"column:applied_sha256;size:64;not null;default:''"`
	AppliedBody        []byte  `gorm:"column:applied_body"`
	AppliedAt          *time.Time
	AppliedRuleCount   int        `gorm:"not null;default:0"`
	AppliedGroups      jsonInt64s `gorm:"column:applied_groups;type:text;not null"`
	AppliedCollect     string     `gorm:"size:16;not null;default:''"`
	CollectEffective   string     `gorm:"size:16;not null;default:''"`
	ResolveEffective   string     `gorm:"column:resolve_effective;size:16;not null;default:''"`
	ResolveUnavailable string     `gorm:"column:resolve_unavailable;size:16;not null;default:''"`
	ReportedSHA256     string     `gorm:"column:reported_sha256;size:64;not null;default:''"`
	ReportedState      string     `gorm:"size:16;not null;default:''"`
	ReportedIssue      string     `gorm:"size:64;not null;default:''"`
	ReportedListeners  *string    `gorm:"column:reported_listeners;type:text"`
	ReportedAt         *time.Time
	RejectedCaps       string `gorm:"size:8;not null;default:''"`
	RejectedSHA256     string `gorm:"column:rejected_sha256;size:64;not null;default:''"`
	MintedGeneration   int64  `gorm:"not null;default:0"`
	MintedCtx          string `gorm:"column:minted_ctx;size:64;not null;default:''"`
	RejectedCtx        string `gorm:"column:rejected_ctx;size:64;not null;default:''"`
	UpdatedAt          time.Time
}

func (destAgentPolicyRow) TableName() string { return "dest_agent_policy" }

func (r destAgentPolicyRow) toDomain() *domain.DestAgentPolicy {
	return &domain.DestAgentPolicy{
		AgentID: r.AgentID, DesiredSHA256: r.DesiredSHA256, MintedSHA256: r.MintedSHA256, MintedAt: utcPtr(r.MintedAt),
		FallbackReason: domain.DestFallbackReason(r.FallbackReason), RejectedGeneration: r.RejectedGeneration,
		OverLimit: derefString(r.OverLimit), PrecheckListeners: derefString(r.PrecheckListeners),
		ReportOnlyShed: r.ReportOnlyShed,
		AppliedSHA256:  r.AppliedSHA256, AppliedBody: r.AppliedBody, AppliedAt: utcPtr(r.AppliedAt),
		AppliedRuleCount: r.AppliedRuleCount, AppliedGroups: nilIfEmptyInt64s(r.AppliedGroups),
		AppliedCollect: r.AppliedCollect, CollectEffective: r.CollectEffective, ResolveEffective: r.ResolveEffective, ResolveUnavailable: r.ResolveUnavailable, ReportedSHA256: r.ReportedSHA256, ReportedState: r.ReportedState,
		ReportedIssue: r.ReportedIssue, ReportedListeners: derefString(r.ReportedListeners),
		ReportedAt: utcPtr(r.ReportedAt), RejectedCaps: r.RejectedCaps, RejectedSHA256: r.RejectedSHA256, MintedGeneration: r.MintedGeneration, MintedCtx: r.MintedCtx, RejectedCtx: r.RejectedCtx, UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// ---- small helpers shared by the destination repos ----

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nilIfEmptyInt64s(in jsonInt64s) []int64 {
	if len(in) == 0 {
		return nil
	}
	return []int64(in)
}

func nilIfEmptyStrings(in jsonStrings) []string {
	if len(in) == 0 {
		return nil
	}
	return []string(in)
}

// truncateUTF8 cuts s to at most n bytes without splitting a character: a
// note column is shortened rather than refusing the write that carries it.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func isHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
