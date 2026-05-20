package mysql

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ---- GORM row types ----
//
// Each row type mirrors a domain entity 1:1 but carries GORM tags and JSON
// wrappers. The service layer only ever touches domain.* types; rows are an
// adapter-internal concern, converted via to/from helpers.

type userRow struct {
	ID  int64  `gorm:"primaryKey;autoIncrement"`
	UPN string `gorm:"size:255;uniqueIndex;not null"`
	// SSOProvider + SSOSubject form the SSO identity. Local accounts
	// carry ('local', UPN); SSO accounts carry
	// ('saml:<name>'|'oidc:<name>', NameID|sub). idx_user_sso is a
	// non-unique composite index for GetBySSO lookups — uniqueness is
	// guarded by EnsureSSO's GetBySSO-first + strict-conflict policy,
	// not by a DB constraint, so a legacy install upgrading from
	// pre-v2.3.0 (every row defaulting to ('local','')) doesn't fail
	// AutoMigrate; first-time SSO login backfills sso_subject on demand.
	SSOProvider         string `gorm:"size:64;not null;default:local;index:idx_user_sso,priority:1"`
	SSOSubject          string `gorm:"size:255;not null;default:'';index:idx_user_sso,priority:2"`
	Email               string `gorm:"size:255;index"`
	PasswordHash        string `gorm:"size:255"`
	Role                string `gorm:"size:16;not null;default:user"`
	SubToken            string `gorm:"size:64;uniqueIndex;not null"`
	UUID                string `gorm:"size:36;not null"`
	GroupID             int64  `gorm:"index;not null"`
	EnabledRuleSets     jsonStrings
	PersonalRules       string `gorm:"type:text"`
	ExpireAt            *time.Time
	TrafficLimitBytes   int64
	TrafficResetPeriod  string `gorm:"size:16;default:never"`
	TrafficPeriodStart  *time.Time
	LifetimeUpBytes     int64 `gorm:"default:0"`
	LifetimeDownBytes   int64 `gorm:"default:0"`
	LifetimeTotalBytes  int64 `gorm:"default:0"`
	// PeriodBaselineBytes: LifetimeTotalBytes at the start of the current
	// period. periodUsage simplifies to lifetime - baseline (O(1)). Pre-v3
	// derived from a LastBefore(period_start) snapshot query on every read.
	PeriodBaselineBytes int64 `gorm:"default:0"`
	LifetimeBaselineAt  *time.Time
	DisplayName         string `gorm:"size:128"`
	Remark              string `gorm:"size:255"`
	Enabled             bool   `gorm:"not null"`
	AutoDisabledReason  string `gorm:"size:32"`
	DisableDetail       string `gorm:"type:text"`
	BlockViolationCount int    `gorm:"default:0"`
	EmergencyUsedCount     int
	EmergencyUntil         *time.Time
	EmergencyBaselineBytes int64 `gorm:"default:0"`
	// TokenVersion bumps invalidate every JWT issued before the bump
	// (admin disable / role demote / password change). Auth middleware
	// rejects a token whose tv claim doesn't match the live row's
	// value. Default 0 so existing rows simply pass the check on a
	// row that hasn't been bumped yet.
	TokenVersion int `gorm:"default:0;not null"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (userRow) TableName() string { return "users" }

func (r *userRow) toDomain() *domain.User {
	return &domain.User{
		ID:                  r.ID,
		UPN:                 r.UPN,
		SSOProvider:         r.SSOProvider,
		SSOSubject:          r.SSOSubject,
		Email:               r.Email,
		PasswordHash:        r.PasswordHash,
		Role:                domain.Role(r.Role),
		SubToken:            r.SubToken,
		UUID:                r.UUID,
		GroupID:             r.GroupID,
		EnabledRuleSets:     []string(r.EnabledRuleSets),
		PersonalRules:       r.PersonalRules,
		ExpireAt:            r.ExpireAt,
		TrafficLimitBytes:   r.TrafficLimitBytes,
		TrafficResetPeriod:  domain.ResetPeriod(r.TrafficResetPeriod),
		TrafficPeriodStart:  r.TrafficPeriodStart,
		LifetimeUpBytes:     r.LifetimeUpBytes,
		LifetimeDownBytes:   r.LifetimeDownBytes,
		LifetimeTotalBytes:  r.LifetimeTotalBytes,
		LifetimeBaselineAt:  r.LifetimeBaselineAt,
		PeriodBaselineBytes: r.PeriodBaselineBytes,
		DisplayName:         r.DisplayName,
		Remark:              r.Remark,
		Enabled:             r.Enabled,
		AutoDisabledReason:  domain.AutoDisabledReason(r.AutoDisabledReason),
		DisableDetail:       r.DisableDetail,
		BlockViolationCount:    r.BlockViolationCount,
		EmergencyUsedCount:     r.EmergencyUsedCount,
		EmergencyUntil:         r.EmergencyUntil,
		EmergencyBaselineBytes: r.EmergencyBaselineBytes,
		TokenVersion:           r.TokenVersion,
		CreatedAt:              r.CreatedAt,
		UpdatedAt:              r.UpdatedAt,
	}
}

func userFromDomain(u *domain.User) *userRow {
	return &userRow{
		ID:                  u.ID,
		UPN:                 u.UPN,
		SSOProvider:         u.SSOProvider,
		SSOSubject:          u.SSOSubject,
		Email:               u.Email,
		PasswordHash:        u.PasswordHash,
		Role:                string(u.Role),
		SubToken:            u.SubToken,
		UUID:                u.UUID,
		GroupID:             u.GroupID,
		EnabledRuleSets:     jsonStrings(u.EnabledRuleSets),
		PersonalRules:       u.PersonalRules,
		ExpireAt:            u.ExpireAt,
		TrafficLimitBytes:   u.TrafficLimitBytes,
		TrafficResetPeriod:  string(u.TrafficResetPeriod),
		TrafficPeriodStart:  u.TrafficPeriodStart,
		LifetimeUpBytes:     u.LifetimeUpBytes,
		LifetimeDownBytes:   u.LifetimeDownBytes,
		LifetimeTotalBytes:  u.LifetimeTotalBytes,
		LifetimeBaselineAt:  u.LifetimeBaselineAt,
		PeriodBaselineBytes: u.PeriodBaselineBytes,
		DisplayName:         u.DisplayName,
		Remark:              u.Remark,
		Enabled:             u.Enabled,
		AutoDisabledReason:  string(u.AutoDisabledReason),
		DisableDetail:       u.DisableDetail,
		BlockViolationCount:    u.BlockViolationCount,
		EmergencyUsedCount:     u.EmergencyUsedCount,
		EmergencyUntil:         u.EmergencyUntil,
		EmergencyBaselineBytes: u.EmergencyBaselineBytes,
		TokenVersion:           u.TokenVersion,
		CreatedAt:              u.CreatedAt,
		UpdatedAt:              u.UpdatedAt,
	}
}

type groupRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	Slug      string `gorm:"size:64;uniqueIndex;not null"`
	Name      string `gorm:"size:128;not null"`
	TagFilter jsonTagFilter
	Layout    jsonLayout
	Remark    string `gorm:"size:255"`
	CreatedAt time.Time
}

// "groups" is a reserved word in some MySQL versions; use groups_ to avoid quoting issues.
func (groupRow) TableName() string { return "groups_" }

func (r *groupRow) toDomain() *domain.Group {
	return &domain.Group{
		ID:        r.ID,
		Slug:      r.Slug,
		Name:      r.Name,
		TagFilter: domain.TagFilter(r.TagFilter),
		Layout:    domain.Layout(r.Layout),
		Remark:    r.Remark,
		CreatedAt: r.CreatedAt,
	}
}

func groupFromDomain(g *domain.Group) *groupRow {
	return &groupRow{
		ID:        g.ID,
		Slug:      g.Slug,
		Name:      g.Name,
		TagFilter: jsonTagFilter(g.TagFilter),
		Layout:    jsonLayout(g.Layout),
		Remark:    g.Remark,
		CreatedAt: g.CreatedAt,
	}
}

type nodeRow struct {
	ID                    int64 `gorm:"primaryKey;autoIncrement"`
	PanelID               int64 `gorm:"not null;index;uniqueIndex:uk_panel_inbound,priority:1"`
	InboundID             int   `gorm:"not null;uniqueIndex:uk_panel_inbound,priority:2"`
	DisplayName           string `gorm:"size:255;not null"`
	ServerAddress         string `gorm:"size:255"`
	Flow                  string `gorm:"size:64"`
	// Protocol caches the upstream inbound's protocol so the UI can gate
	// protocol-specific fields without a live 3X-UI fetch. Empty for rows
	// written before this column existed; AutoMigrate adds it, no backfill.
	Protocol              string `gorm:"size:32;default:''"`
	Region                string `gorm:"size:16;not null"`
	Tags                  jsonStrings
	SortOrder             int    `gorm:"default:0"`
	Enabled               bool   `gorm:"default:true"`
	// Kind discriminates real 3X-UI-backed nodes from layout-only
	// separator entries. Empty (the default for rows written before this
	// column existed) is treated as "real" by the toDomain mapping so
	// AutoMigrate alone is enough — no backfill needed.
	Kind                  string `gorm:"size:16;default:'real'"`
	LifetimeUpBytes       int64 `gorm:"default:0"`
	LifetimeDownBytes     int64 `gorm:"default:0"`
	LifetimeTotalBytes    int64 `gorm:"default:0"`
	LastTrafficUpBytes    int64  `gorm:"default:0"`
	LastTrafficDownBytes  int64  `gorm:"default:0"`
	LastTrafficTotalBytes int64  `gorm:"default:0"`
	HealthState           string `gorm:"size:32;default:''"`
	HealthCheckedAt       *time.Time
	HealthDetail          string `gorm:"size:512;default:''"`
	CreatedAt             time.Time
}

func (nodeRow) TableName() string { return "nodes" }

func (r *nodeRow) toDomain() *domain.Node {
	kind := domain.NodeKind(r.Kind)
	if kind == "" {
		kind = domain.NodeKindReal
	}
	return &domain.Node{
		ID:                    r.ID,
		PanelID:               r.PanelID,
		InboundID:             r.InboundID,
		DisplayName:           r.DisplayName,
		ServerAddress:         r.ServerAddress,
		Flow:                  r.Flow,
		Protocol:              r.Protocol,
		Region:                r.Region,
		Tags:                  []string(r.Tags),
		SortOrder:             r.SortOrder,
		Enabled:               r.Enabled,
		Kind:                  kind,
		LifetimeUpBytes:       r.LifetimeUpBytes,
		LifetimeDownBytes:     r.LifetimeDownBytes,
		LifetimeTotalBytes:    r.LifetimeTotalBytes,
		LastTrafficUpBytes:    r.LastTrafficUpBytes,
		LastTrafficDownBytes:  r.LastTrafficDownBytes,
		LastTrafficTotalBytes: r.LastTrafficTotalBytes,
		HealthState:           domain.NodeHealthState(r.HealthState),
		HealthCheckedAt:       r.HealthCheckedAt,
		HealthDetail:          r.HealthDetail,
		CreatedAt:             r.CreatedAt,
	}
}

func nodeFromDomain(n *domain.Node) *nodeRow {
	kind := n.Kind
	if kind == "" {
		kind = domain.NodeKindReal
	}
	return &nodeRow{
		ID:                    n.ID,
		PanelID:               n.PanelID,
		InboundID:             n.InboundID,
		DisplayName:           n.DisplayName,
		ServerAddress:         n.ServerAddress,
		Flow:                  n.Flow,
		Protocol:              n.Protocol,
		Region:                n.Region,
		Tags:                  jsonStrings(n.Tags),
		SortOrder:             n.SortOrder,
		Enabled:               n.Enabled,
		Kind:                  string(kind),
		LifetimeUpBytes:       n.LifetimeUpBytes,
		LifetimeDownBytes:     n.LifetimeDownBytes,
		LifetimeTotalBytes:    n.LifetimeTotalBytes,
		LastTrafficUpBytes:    n.LastTrafficUpBytes,
		LastTrafficDownBytes:  n.LastTrafficDownBytes,
		LastTrafficTotalBytes: n.LastTrafficTotalBytes,
		HealthState:           string(n.HealthState),
		HealthCheckedAt:       n.HealthCheckedAt,
		HealthDetail:          n.HealthDetail,
		CreatedAt:             n.CreatedAt,
	}
}

type ownershipRow struct {
	ID          int64 `gorm:"primaryKey;autoIncrement"`
	UserID      int64 `gorm:"index;not null"`
	PanelID     int64 `gorm:"not null;index;uniqueIndex:uk_owner_match,priority:1"`
	InboundID   int   `gorm:"not null;uniqueIndex:uk_owner_match,priority:2"`
	ClientEmail string `gorm:"size:255;not null;uniqueIndex:uk_owner_match,priority:3"`
	ClientUUID  string `gorm:"size:36;not null"`
	CreatedAt   time.Time
	// Lifetime counters accumulate monotonically across 3X-UI counter
	// resets, mirroring the same fields on users / nodes. The traffic poll
	// derives the per-cycle delta as monotonicDelta(current_raw, LastRawXxx),
	// updates LastRawXxx to the new raw value, and adds the delta to
	// LifetimeXxx in one transaction. Per-client lifetime makes "top clients
	// by all-time usage" a single ORDER BY rather than a snapshot-table scan.
	LifetimeUpBytes    int64 `gorm:"default:0"`
	LifetimeDownBytes  int64 `gorm:"default:0"`
	LifetimeTotalBytes int64 `gorm:"default:0"`
	// LastRawXxx records the most recently observed raw 3X-UI cumulative
	// counter. Treated as 0 on the first poll for a freshly-imported client
	// (in that case the full current cumulative becomes the initial delta,
	// matching the prior LatestForClient-based bootstrap).
	LastRawUpBytes    int64 `gorm:"default:0"`
	LastRawDownBytes  int64 `gorm:"default:0"`
	LastRawTotalBytes int64 `gorm:"default:0"`
}

// user_xui_clients (renamed from xui_clients in v3): per-local-user ownership
// of a 3X-UI client row identified by (panel_id, inbound_id, client_email).
// The "user_" prefix makes the join-table semantics explicit — this is not a
// cache of 3X-UI's own client list.
func (ownershipRow) TableName() string { return "user_xui_clients" }

func (r *ownershipRow) toDomain() *domain.XUIClientEntry {
	return &domain.XUIClientEntry{
		ID:                 r.ID,
		UserID:             r.UserID,
		PanelID:            r.PanelID,
		InboundID:          r.InboundID,
		ClientEmail:        r.ClientEmail,
		ClientUUID:         r.ClientUUID,
		CreatedAt:          r.CreatedAt,
		LifetimeUpBytes:    r.LifetimeUpBytes,
		LifetimeDownBytes:  r.LifetimeDownBytes,
		LifetimeTotalBytes: r.LifetimeTotalBytes,
		LastRawUpBytes:     r.LastRawUpBytes,
		LastRawDownBytes:   r.LastRawDownBytes,
		LastRawTotalBytes:  r.LastRawTotalBytes,
	}
}

func ownershipFromDomain(e *domain.XUIClientEntry) *ownershipRow {
	return &ownershipRow{
		ID:                 e.ID,
		UserID:             e.UserID,
		PanelID:            e.PanelID,
		InboundID:          e.InboundID,
		ClientEmail:        e.ClientEmail,
		ClientUUID:         e.ClientUUID,
		CreatedAt:          e.CreatedAt,
		LifetimeUpBytes:    e.LifetimeUpBytes,
		LifetimeDownBytes:  e.LifetimeDownBytes,
		LifetimeTotalBytes: e.LifetimeTotalBytes,
		LastRawUpBytes:     e.LastRawUpBytes,
		LastRawDownBytes:   e.LastRawDownBytes,
		LastRawTotalBytes:  e.LastRawTotalBytes,
	}
}

type trafficRow struct {
	ID         int64 `gorm:"primaryKey;autoIncrement"`
	UserID     int64 `gorm:"not null;index:idx_user_time,priority:1"`
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
	// captured_at carries TWO indexes:
	//   idx_user_time (user_id, captured_at) — covers per-user queries (Latest,
	//     LastBefore, ListByUser).
	//   idx_traffic_captured (captured_at) — covers the retention DELETE
	//     (WHERE captured_at < cutoff). idx_user_time can't serve that query
	//     because user_id is the leading column, so without this second index
	//     the hourly prune degenerates to a full table scan once the table
	//     grows past a few hundred thousand rows.
	CapturedAt time.Time `gorm:"not null;index:idx_user_time,priority:2;index:idx_traffic_captured"`
}

func (trafficRow) TableName() string { return "traffic_snapshots" }

func (r *trafficRow) toDomain() *domain.TrafficSnapshot {
	return &domain.TrafficSnapshot{
		ID:         r.ID,
		UserID:     r.UserID,
		UpBytes:    r.UpBytes,
		DownBytes:  r.DownBytes,
		TotalBytes: r.TotalBytes,
		CapturedAt: r.CapturedAt,
	}
}

type clientTrafficRow struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	UserID      int64  `gorm:"not null;index:idx_client_time,priority:1"`
	PanelID     int64  `gorm:"not null;index:idx_client_time,priority:2"`
	InboundID   int    `gorm:"not null;index:idx_client_time,priority:3"`
	ClientEmail string `gorm:"size:255;not null;index:idx_client_time,priority:4"`
	UpBytes     int64
	DownBytes   int64
	TotalBytes  int64
	// See trafficRow.CapturedAt for the second-index rationale —
	// client_traffic_snapshots is the largest of the three time-series tables,
	// so the dedicated captured_at index matters most here.
	CapturedAt time.Time `gorm:"not null;index:idx_client_time,priority:5;index:idx_client_traffic_captured"`
}

func (clientTrafficRow) TableName() string { return "client_traffic_snapshots" }

func (r *clientTrafficRow) toDomain() *domain.ClientTrafficSnapshot {
	return &domain.ClientTrafficSnapshot{
		ID:          r.ID,
		UserID:      r.UserID,
		PanelID:     r.PanelID,
		InboundID:   r.InboundID,
		ClientEmail: r.ClientEmail,
		UpBytes:     r.UpBytes,
		DownBytes:   r.DownBytes,
		TotalBytes:  r.TotalBytes,
		CapturedAt:  r.CapturedAt,
	}
}

type nodeTrafficRow struct {
	ID         int64 `gorm:"primaryKey;autoIncrement"`
	NodeID     int64 `gorm:"not null;index:idx_node_time,priority:1"`
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
	CapturedAt time.Time `gorm:"not null;index:idx_node_time,priority:2;index:idx_node_traffic_captured"`
}

func (nodeTrafficRow) TableName() string { return "node_traffic_snapshots" }

func (r *nodeTrafficRow) toDomain() *domain.NodeTrafficSnapshot {
	return &domain.NodeTrafficSnapshot{
		ID:         r.ID,
		NodeID:     r.NodeID,
		UpBytes:    r.UpBytes,
		DownBytes:  r.DownBytes,
		TotalBytes: r.TotalBytes,
		CapturedAt: r.CapturedAt,
	}
}

// ---- Hourly rollup tables (v3.0.0-beta.6+) -----------------------------
//
// Each *_hourly table stores per-entity *delta* within a 1-hour UTC bucket.
// Populated by the rollup service from the raw 5-min snapshot tables, then
// served to chart queries that want Day/Week/Month granularity in any
// caller-provided timezone — the query layer fetches the UTC range, then
// converts and groups by local day/week/month in Go.
//
// Idempotency: the (entity_id, ..., bucket_start) unique index lets rollup
// be re-run safely (ON DUPLICATE KEY UPDATE / ON CONFLICT DO UPDATE),
// which is what makes the first run after upgrade act as a backfill.

type trafficHourlyRow struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	UserID      int64     `gorm:"not null;uniqueIndex:uk_user_bucket_h,priority:1"`
	BucketStart time.Time `gorm:"not null;uniqueIndex:uk_user_bucket_h,priority:2;index:idx_traffic_hourly_bucket"`
	UpBytes     int64
	DownBytes   int64
	TotalBytes  int64
}

func (trafficHourlyRow) TableName() string { return "traffic_snapshots_hourly" }

type clientTrafficHourlyRow struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	UserID      int64     `gorm:"not null;index:idx_client_hourly_user"`
	PanelID     int64     `gorm:"not null;uniqueIndex:uk_client_bucket_h,priority:1"`
	InboundID   int       `gorm:"not null;uniqueIndex:uk_client_bucket_h,priority:2"`
	ClientEmail string    `gorm:"size:255;not null;uniqueIndex:uk_client_bucket_h,priority:3"`
	BucketStart time.Time `gorm:"not null;uniqueIndex:uk_client_bucket_h,priority:4;index:idx_client_hourly_bucket"`
	UpBytes     int64
	DownBytes   int64
	TotalBytes  int64
}

func (clientTrafficHourlyRow) TableName() string { return "client_traffic_snapshots_hourly" }

type nodeTrafficHourlyRow struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	NodeID      int64     `gorm:"not null;uniqueIndex:uk_node_bucket_h,priority:1"`
	BucketStart time.Time `gorm:"not null;uniqueIndex:uk_node_bucket_h,priority:2;index:idx_node_hourly_bucket"`
	UpBytes     int64
	DownBytes   int64
	TotalBytes  int64
}

func (nodeTrafficHourlyRow) TableName() string { return "node_traffic_snapshots_hourly" }

type auditRow struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	Actor      string    `gorm:"size:255;not null"`
	Action     string    `gorm:"size:64;not null"`
	Target     string    `gorm:"size:255"`
	// Stored as plain text, not a JSON column: these hold opaque serialized
	// snapshots that are never queried with JSON operators, and the audit
	// helpers legitimately write "" (e.g. a create has no before-state).
	// Postgres' json type rejects the empty string, so text is the portable
	// choice across sqlite / mysql / postgres.
	BeforeJSON string    `gorm:"type:text"`
	AfterJSON  string    `gorm:"type:text"`
	IP         string    `gorm:"size:64"`
	At         time.Time `gorm:"index"`
}

func (auditRow) TableName() string { return "audit_log" }

func (r *auditRow) toDomain() *domain.AuditEntry {
	return &domain.AuditEntry{
		ID:         r.ID,
		Actor:      r.Actor,
		Action:     r.Action,
		Target:     r.Target,
		BeforeJSON: r.BeforeJSON,
		AfterJSON:  r.AfterJSON,
		IP:         r.IP,
		At:         r.At,
	}
}

type subLogRow struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	UserID     int64     `gorm:"index"`
	IP         string    `gorm:"size:64"`
	UA         string    `gorm:"size:255"`
	ClientType string    `gorm:"size:32"`
	AccessedAt time.Time `gorm:"index"`
}

type syncTaskRow struct {
	ID         int64  `gorm:"primaryKey;autoIncrement"`
	Type       string `gorm:"size:64;not null;index:idx_task_due,priority:1"`
	Status     string `gorm:"size:32;not null;index:idx_task_due,priority:2"`
	TargetType string `gorm:"size:64;not null;index:idx_task_target,priority:1"`
	TargetID   int64  `gorm:"not null;index:idx_task_target,priority:2"`
	Summary    string `gorm:"size:255"`
	Payload    string `gorm:"type:text"`
	LastError  string `gorm:"type:text"`
	Attempts   int
	NextRunAt  time.Time `gorm:"index:idx_task_due,priority:3"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt *time.Time
}

func (syncTaskRow) TableName() string { return "sync_tasks" }

func (r *syncTaskRow) toDomain() *domain.SyncTask {
	return &domain.SyncTask{
		ID:         r.ID,
		Type:       domain.SyncTaskType(r.Type),
		Status:     domain.SyncTaskStatus(r.Status),
		TargetType: r.TargetType,
		TargetID:   r.TargetID,
		Summary:    r.Summary,
		Payload:    r.Payload,
		LastError:  r.LastError,
		Attempts:   r.Attempts,
		NextRunAt:  r.NextRunAt,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
		FinishedAt: r.FinishedAt,
	}
}

func syncTaskFromDomain(t *domain.SyncTask) *syncTaskRow {
	return &syncTaskRow{
		ID:         t.ID,
		Type:       string(t.Type),
		Status:     string(t.Status),
		TargetType: t.TargetType,
		TargetID:   t.TargetID,
		Summary:    t.Summary,
		Payload:    t.Payload,
		LastError:  t.LastError,
		Attempts:   t.Attempts,
		NextRunAt:  t.NextRunAt,
		CreatedAt:  t.CreatedAt,
		UpdatedAt:  t.UpdatedAt,
		FinishedAt: t.FinishedAt,
	}
}

type xuiPanelRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	Name      string `gorm:"size:128;uniqueIndex;not null"`
	URL       string `gorm:"size:512;not null"`
	APIToken  string `gorm:"type:text"`
	Username  string `gorm:"size:255"`
	Password  string `gorm:"type:text"`
	Remark    string `gorm:"size:255"`
	CreatedAt time.Time
	UpdatedAt time.Time
}


type mailSettingsRow struct {
	ID           int64 `gorm:"primaryKey"`
	Enabled      bool
	SMTPHost     string `gorm:"size:255"`
	SMTPPort     int
	SMTPUsername string `gorm:"size:255"`
	SMTPPassword string `gorm:"type:text"`
	FromEmail    string `gorm:"size:255"`
	FromName     string `gorm:"size:128"`
	Encryption   string `gorm:"size:16"`
	UpdatedAt    time.Time
}

func (mailSettingsRow) TableName() string { return "mail_settings" }

func (r *mailSettingsRow) toDomain() (domain.MailSettings, error) {
	password, err := decryptSecret(r.SMTPPassword)
	if err != nil {
		return domain.MailSettings{}, err
	}
	return domain.MailSettings{
		Enabled:      r.Enabled,
		SMTPHost:     r.SMTPHost,
		SMTPPort:     r.SMTPPort,
		SMTPUsername: r.SMTPUsername,
		SMTPPassword: password,
		FromEmail:    r.FromEmail,
		FromName:     r.FromName,
		Encryption:   r.Encryption,
	}, nil
}

func mailSettingsFromDomain(s domain.MailSettings) (*mailSettingsRow, error) {
	password, err := encryptSecret(s.SMTPPassword)
	if err != nil {
		return nil, err
	}
	return &mailSettingsRow{
		ID:           1,
		Enabled:      s.Enabled,
		SMTPHost:     s.SMTPHost,
		SMTPPort:     s.SMTPPort,
		SMTPUsername: s.SMTPUsername,
		SMTPPassword: password,
		FromEmail:    s.FromEmail,
		FromName:     s.FromName,
		Encryption:   s.Encryption,
	}, nil
}

type mailTemplateRow struct {
	Kind      string `gorm:"size:32;primaryKey"`
	Subject   string `gorm:"size:255"`
	Body      string `gorm:"type:text"`
	Enabled   bool
	UpdatedAt time.Time
}

func (mailTemplateRow) TableName() string { return "mail_templates" }

func (r *mailTemplateRow) toDomain() *domain.MailTemplate {
	return &domain.MailTemplate{
		Kind:      domain.MailReminderKind(r.Kind),
		Subject:   r.Subject,
		Body:      r.Body,
		Enabled:   r.Enabled,
		UpdatedAt: r.UpdatedAt,
	}
}

func mailTemplateFromDomain(t *domain.MailTemplate) *mailTemplateRow {
	return &mailTemplateRow{
		Kind:    string(t.Kind),
		Subject: t.Subject,
		Body:    t.Body,
		Enabled: t.Enabled,
	}
}

type mailSentRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	UserID    int64  `gorm:"not null;uniqueIndex:uk_mail_once,priority:1"`
	Kind      string `gorm:"size:32;not null;uniqueIndex:uk_mail_once,priority:2"`
	WindowKey string `gorm:"size:128;not null;uniqueIndex:uk_mail_once,priority:3"`
	ToEmail   string `gorm:"size:255;not null"`
	SentAt    time.Time
}

func (mailSentRow) TableName() string { return "mail_sent" }

func (xuiPanelRow) TableName() string { return "xui_panels" }

func (r *xuiPanelRow) toDomain() (*domain.XUIPanel, error) {
	apiToken, err := decryptSecret(r.APIToken)
	if err != nil {
		return nil, err
	}
	password, err := decryptSecret(r.Password)
	if err != nil {
		return nil, err
	}
	return &domain.XUIPanel{
		ID:       r.ID,
		Name:     r.Name,
		URL:      r.URL,
		APIToken: apiToken,
		Username: r.Username,
		Password: password,
		Remark:   r.Remark,
	}, nil
}

func xuiPanelFromDomain(p *domain.XUIPanel) (*xuiPanelRow, error) {
	apiToken, err := encryptSecret(p.APIToken)
	if err != nil {
		return nil, err
	}
	password, err := encryptSecret(p.Password)
	if err != nil {
		return nil, err
	}
	return &xuiPanelRow{
		ID:       p.ID,
		Name:     p.Name,
		URL:      p.URL,
		APIToken: apiToken,
		Username: p.Username,
		Password: password,
		Remark:   p.Remark,
	}, nil
}

func (subLogRow) TableName() string { return "sub_logs" }

func (r *subLogRow) toDomain() *domain.SubLog {
	return &domain.SubLog{
		ID:         r.ID,
		UserID:     r.UserID,
		IP:         r.IP,
		UA:         r.UA,
		ClientType: r.ClientType,
		AccessedAt: r.AccessedAt,
	}
}

// separatorRow stores subscription-list separators (the "----- Taiwan -----"
// decoration rows). Lives in its own table so node-iterating workers
// (traffic / health / reconcile) never see them and don't need a runtime
// IsSeparator() check on every row. Replaces the pre-v3.0.0-beta.7 model
// of mixing separators into `nodes` with a `kind` column + a synthetic
// negative inbound_id; legacy rows are cleaned up by cleanupLegacyState.
type separatorRow struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	DisplayName string `gorm:"size:255;not null"`
	SortOrder   int    `gorm:"default:0"`
	Enabled     bool   `gorm:"default:true"`
	// Mode replaces the legacy show_in_all_groups bool. "global" =
	// visible everywhere; "node_bound" = visible only when the group
	// being rendered contains at least one node listed in NodeIDs.
	Mode string `gorm:"size:32;not null;default:global"`
	// NodeIDs is the gate set when Mode='node_bound'. Empty under that
	// mode means "never visible" (explicit hidden state).
	NodeIDs   jsonInt64s
	CreatedAt time.Time
}

func (separatorRow) TableName() string { return "nodes_separator" }

func (r *separatorRow) toDomain() *domain.SeparatorEntry {
	mode := domain.SeparatorMode(r.Mode)
	// Coerce stray / empty values to the safe default. AutoMigrate sets
	// the column default to "global", but rows mid-upgrade or hand-edited
	// by an admin could carry anything.
	if mode != domain.SeparatorModeGlobal && mode != domain.SeparatorModeNodeBound {
		mode = domain.SeparatorModeGlobal
	}
	return &domain.SeparatorEntry{
		ID:          r.ID,
		DisplayName: r.DisplayName,
		SortOrder:   r.SortOrder,
		Enabled:     r.Enabled,
		Mode:        mode,
		NodeIDs:     []int64(r.NodeIDs),
		CreatedAt:   r.CreatedAt,
	}
}

func separatorFromDomain(s *domain.SeparatorEntry) *separatorRow {
	mode := s.Mode
	if mode != domain.SeparatorModeGlobal && mode != domain.SeparatorModeNodeBound {
		mode = domain.SeparatorModeGlobal
	}
	return &separatorRow{
		ID:          s.ID,
		DisplayName: s.DisplayName,
		SortOrder:   s.SortOrder,
		Enabled:     s.Enabled,
		Mode:        string(mode),
		NodeIDs:     jsonInt64s(s.NodeIDs),
		CreatedAt:   s.CreatedAt,
	}
}

// ---- JSON field wrappers (driver.Valuer / sql.Scanner) ----

// jsonInt64s persists []int64 as a JSON array column. Mirrors jsonStrings
// — used for separatorRow.GroupIDs.
type jsonInt64s []int64

func (j jsonInt64s) Value() (driver.Value, error) {
	if j == nil {
		return "[]", nil
	}
	b, err := json.Marshal(j)
	return string(b), err
}

func (j *jsonInt64s) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("unsupported scan type for jsonInt64s: %T", value)
	}
	if len(b) == 0 {
		*j = nil
		return nil
	}
	return json.Unmarshal(b, j)
}

type jsonStrings []string

func (j jsonStrings) Value() (driver.Value, error) {
	if j == nil {
		return "[]", nil
	}
	b, err := json.Marshal(j)
	return string(b), err
}

func (j *jsonStrings) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("unsupported scan type for jsonStrings: %T", value)
	}
	if len(b) == 0 {
		*j = nil
		return nil
	}
	return json.Unmarshal(b, j)
}

// jsonRoleRules persists []config.SSORoleRule as a JSON blob in the
// saml_settings / oidc_settings table. Same shape as jsonStrings — Value()
// returns "[]" for nil so the DB column stays NOT NULL.
type jsonRoleRules []config.SSORoleRule

func (j jsonRoleRules) Value() (driver.Value, error) {
	if j == nil {
		return "[]", nil
	}
	b, err := json.Marshal(j)
	return string(b), err
}

func (j *jsonRoleRules) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("unsupported scan type for jsonRoleRules: %T", value)
	}
	if len(b) == 0 {
		*j = nil
		return nil
	}
	return json.Unmarshal(b, j)
}

type jsonTagFilter domain.TagFilter

func (j jsonTagFilter) Value() (driver.Value, error) {
	b, err := json.Marshal(domain.TagFilter(j))
	return string(b), err
}

func (j *jsonTagFilter) Scan(value any) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("unsupported scan type for jsonTagFilter: %T", value)
	}
	return json.Unmarshal(b, (*domain.TagFilter)(j))
}

type jsonLayout domain.Layout

func (j jsonLayout) Value() (driver.Value, error) {
	b, err := json.Marshal(domain.Layout(j))
	return string(b), err
}

func (j *jsonLayout) Scan(value any) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("unsupported scan type for jsonLayout: %T", value)
	}
	return json.Unmarshal(b, (*domain.Layout)(j))
}

// ---- Schema ----

// EnsureSchema keeps the database schema aligned with the current row structs.
// Keep schema changes centralized here instead of adding one-off schema update
// helpers for every new field.
func EnsureSchema(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&userRow{},
		&groupRow{},
		&nodeRow{},
		&ownershipRow{},
		&trafficRow{},
		&clientTrafficRow{},
		&nodeTrafficRow{},
		&trafficHourlyRow{},
		&clientTrafficHourlyRow{},
		&nodeTrafficHourlyRow{},
		&auditRow{},
		&subLogRow{},
		&syncTaskRow{},
		&xuiPanelRow{},
		&separatorRow{},
		&settingRow{},
		&mailSettingsRow{},
		&mailTemplateRow{},
		&mailSentRow{},
		&samlConfigRow{},
		&oidcConfigRow{},
	); err != nil {
		return err
	}
	if err := backfillTrafficCounterNulls(db); err != nil {
		return err
	}
	return cleanupLegacyState(db)
}

// cleanupLegacyState is the curated home for "one-time cleanups after a
// breaking same-major schema evolution". Mirrors how MariaDB / Postgres
// ship `pg_upgrade` finalize steps — every block is idempotent, version-
// tagged, and gets evicted when the next major version ships (per
// docs/ARCHITECTURE.md §16.4).
//
// Rules:
//  1. Idempotent: re-running on a clean DB MUST be a no-op
//  2. Version-tagged in the comment: makes the v(N+1) reset trivially safe
//  3. NEVER auto-DROP an unknown table or column — admin custom state is
//     out of scope; we only touch what we explicitly removed
//  4. log.Warn when a block actually fires, so an upgrade leaves an
//     audit trail in docker logs
func cleanupLegacyState(db *gorm.DB) error {
	// v3.0.0-beta.7: separators moved out of `nodes` (where they lived as
	// rows with kind='separator' + a synthetic negative inbound_id) into
	// the dedicated `nodes_separator` table. Drop any leftover rows so
	// the post-upgrade panel doesn't show ghost separators that the new
	// CRUD has no idea how to edit. Admins recreate under the new model.
	var legacySeparators int64
	if err := db.Model(&nodeRow{}).Where("kind = ?", "separator").Count(&legacySeparators).Error; err != nil {
		// kind column might not exist on a very old install — that's fine,
		// such installs go through `psp migrate` first which doesn't carry
		// the column forward.
		return nil
	}
	if legacySeparators > 0 {
		if err := db.Where("kind = ?", "separator").Delete(&nodeRow{}).Error; err != nil {
			return fmt.Errorf("cleanup legacy separators: %w", err)
		}
		fmt.Printf("[cleanupLegacyState] dropped %d legacy kind='separator' rows from `nodes`; recreate under the new `nodes_separator` table\n", legacySeparators)
	}

	// v3.0.0-rc.4: separator visibility model reshaped.
	//   show_in_all_groups (bool) → mode (string: "global" / "node_bound")
	//   group_ids (jsonInt64s)     → node_ids (jsonInt64s)
	// AutoMigrate adds the new columns (with default mode="global") above;
	// here we drop the now-unused legacy columns so prod libs don't carry
	// orphan storage indefinitely. Existing rows surface as Mode=global —
	// the prior "show_in_all_groups=false + group_ids=[...]" semantic
	// translates to the safest default ("show everywhere") and the admin
	// re-picks node_ids in the UI if they want node-bound visibility.
	// Idempotent: each DropColumn guarded by HasColumn.
	if db.Migrator().HasColumn(&separatorRow{}, "show_in_all_groups") {
		fmt.Println("[cleanupLegacyState] dropping legacy column nodes_separator.show_in_all_groups (replaced by `mode`)")
		if err := db.Migrator().DropColumn(&separatorRow{}, "show_in_all_groups"); err != nil {
			return fmt.Errorf("drop legacy show_in_all_groups: %w", err)
		}
	}
	if db.Migrator().HasColumn(&separatorRow{}, "group_ids") {
		fmt.Println("[cleanupLegacyState] dropping legacy column nodes_separator.group_ids (replaced by `node_ids`)")
		if err := db.Migrator().DropColumn(&separatorRow{}, "group_ids"); err != nil {
			return fmt.Errorf("drop legacy group_ids: %w", err)
		}
	}

	return nil
}

func backfillTrafficCounterNulls(db *gorm.DB) error {
	if err := db.Exec(`
UPDATE users
SET
	lifetime_up_bytes = COALESCE(lifetime_up_bytes, 0),
	lifetime_down_bytes = COALESCE(lifetime_down_bytes, 0),
	lifetime_total_bytes = COALESCE(lifetime_total_bytes, 0)
`).Error; err != nil {
		return err
	}
	if err := db.Exec(`
UPDATE nodes
SET
	lifetime_up_bytes = COALESCE(lifetime_up_bytes, 0),
	lifetime_down_bytes = COALESCE(lifetime_down_bytes, 0),
	lifetime_total_bytes = COALESCE(lifetime_total_bytes, 0),
	last_traffic_up_bytes = COALESCE(last_traffic_up_bytes, 0),
	last_traffic_down_bytes = COALESCE(last_traffic_down_bytes, 0),
	last_traffic_total_bytes = COALESCE(last_traffic_total_bytes, 0)
`).Error; err != nil {
		return err
	}
	return nil
}

// wrapNotFound maps GORM's ErrRecordNotFound to domain.ErrNotFound so that
// service-layer code can rely on a single sentinel.
func wrapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ErrNotFound
	}
	return err
}
