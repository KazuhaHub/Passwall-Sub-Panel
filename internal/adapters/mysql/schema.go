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
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
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
	CreatedAt              time.Time
	UpdatedAt              time.Time
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
		DisplayName:         r.DisplayName,
		Remark:              r.Remark,
		Enabled:             r.Enabled,
		AutoDisabledReason:  domain.AutoDisabledReason(r.AutoDisabledReason),
		DisableDetail:       r.DisableDetail,
		BlockViolationCount:    r.BlockViolationCount,
		EmergencyUsedCount:     r.EmergencyUsedCount,
		EmergencyUntil:         r.EmergencyUntil,
		EmergencyBaselineBytes: r.EmergencyBaselineBytes,
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
		DisplayName:         u.DisplayName,
		Remark:              u.Remark,
		Enabled:             u.Enabled,
		AutoDisabledReason:  string(u.AutoDisabledReason),
		DisableDetail:       u.DisableDetail,
		BlockViolationCount:    u.BlockViolationCount,
		EmergencyUsedCount:     u.EmergencyUsedCount,
		EmergencyUntil:         u.EmergencyUntil,
		EmergencyBaselineBytes: u.EmergencyBaselineBytes,
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
	ID                    int64  `gorm:"primaryKey;autoIncrement"`
	PanelID               int64  `gorm:"not null;index;uniqueIndex:uk_panel_inbound,priority:1"`
	PanelName             string `gorm:"size:64;index"`
	InboundID             int    `gorm:"not null;uniqueIndex:uk_panel_inbound,priority:2"`
	DisplayName           string `gorm:"size:255;not null"`
	ServerAddress         string `gorm:"size:255"`
	Flow                  string `gorm:"size:64"`
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
		PanelName:             r.PanelName,
		InboundID:             r.InboundID,
		DisplayName:           r.DisplayName,
		ServerAddress:         r.ServerAddress,
		Flow:                  r.Flow,
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
		PanelName:             n.PanelName,
		InboundID:             n.InboundID,
		DisplayName:           n.DisplayName,
		ServerAddress:         n.ServerAddress,
		Flow:                  n.Flow,
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
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	UserID      int64  `gorm:"index;not null"`
	PanelID     int64  `gorm:"not null;index;uniqueIndex:uk_owner_match,priority:1"`
	PanelName   string `gorm:"size:64;index"`
	InboundID   int    `gorm:"not null;uniqueIndex:uk_owner_match,priority:2"`
	ClientEmail string `gorm:"size:255;not null;uniqueIndex:uk_owner_match,priority:3"`
	ClientUUID  string `gorm:"size:36;not null"`
	CreatedAt   time.Time
}

func (ownershipRow) TableName() string { return "xui_clients" }

func (r *ownershipRow) toDomain() *domain.XUIClientEntry {
	return &domain.XUIClientEntry{
		ID:          r.ID,
		UserID:      r.UserID,
		PanelID:     r.PanelID,
		PanelName:   r.PanelName,
		InboundID:   r.InboundID,
		ClientEmail: r.ClientEmail,
		ClientUUID:  r.ClientUUID,
		CreatedAt:   r.CreatedAt,
	}
}

func ownershipFromDomain(e *domain.XUIClientEntry) *ownershipRow {
	return &ownershipRow{
		ID:          e.ID,
		UserID:      e.UserID,
		PanelID:     e.PanelID,
		PanelName:   e.PanelName,
		InboundID:   e.InboundID,
		ClientEmail: e.ClientEmail,
		ClientUUID:  e.ClientUUID,
		CreatedAt:   e.CreatedAt,
	}
}

type trafficRow struct {
	ID         int64 `gorm:"primaryKey;autoIncrement"`
	UserID     int64 `gorm:"not null;index:idx_user_time,priority:1"`
	UpBytes    int64
	DownBytes  int64
	TotalBytes int64
	CapturedAt time.Time `gorm:"not null;index:idx_user_time,priority:2"`
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
	CapturedAt  time.Time `gorm:"not null;index:idx_client_time,priority:5"`
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
	CapturedAt time.Time `gorm:"not null;index:idx_node_time,priority:2"`
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

type auditRow struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	Actor      string    `gorm:"size:255;not null"`
	Action     string    `gorm:"size:64;not null"`
	Target     string    `gorm:"size:255"`
	BeforeJSON string    `gorm:"type:json"`
	AfterJSON  string    `gorm:"type:json"`
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

type uiSettingsRow struct {
	ID                 int64  `gorm:"primaryKey"`
	LoginMode          string `gorm:"size:32"`
	SiteTitle          string `gorm:"size:128"`
	AppTitle           string `gorm:"size:128"`
	IconURL            string `gorm:"size:1024"`
	LogoURL            string `gorm:"size:1024"`
	LogoURLDark        string `gorm:"size:1024"`
	EmailDomain        string `gorm:"size:255"`
	AuditRetentionDays int
	SubBaseURL         string `gorm:"size:512"`
	Timezone           string `gorm:"size:64"`
	// Runtime tuning (restart required to take effect).
	CronTrafficPullMinutes     int
	CronReconcileMinutes       int
	MaxPanelConcurrency        int
	JWTAccessTTLMinutes        int
	JWTRefreshTTLMinutes       int
	JWTIssuer                  string `gorm:"size:128"`
	SubPerIPPerMin             int
	LoginPerIPPerMin           int
	SyncTaskRetentionDays      int
	DisallowUserLocalLogin     bool
	DisallowUserPasswordChange bool
	AllowUserPersonalRules     bool
	EmergencyAccessEnabled     bool
	EmergencyAccessHours       int
	EmergencyAccessMaxCount    int
	EmergencyAccessQuotaGB     int
	// Subscription settings
	SubPath                  string                 `gorm:"size:128;default:'sub'"`
	SubClientRules           jsonSubRules           `gorm:"type:json"`
	SubImportClients         jsonSubImportClients   `gorm:"type:json"`
	SubImportTutorialURL     string                 `gorm:"size:512"`
	SubLogRetentionDays      int                    `gorm:"default:7"`
	SubBlockAutoDisable      bool                   `gorm:"default:false"`
	SubBlockAutoDisableCount int                    `gorm:"default:3"`
	SubUpdateIntervalHours   int                    `gorm:"default:24"`
	SubRegionFlagPrefix      bool                   `gorm:"default:false"`
	QuickLinks               jsonQuickLinks         `gorm:"type:json"`
	GlobalAnnouncement       jsonGlobalAnnouncement `gorm:"type:json"`
	FooterText               string                 `gorm:"size:255"`
	ThemeColor               string                 `gorm:"size:16"`
	UpdatedAt                time.Time
}

// jsonSubRules is a JSON wrapper for []ports.SubClientRule.
type jsonSubRules []ports.SubClientRule

func (j jsonSubRules) toDomain() []ports.SubClientRule {
	if j == nil {
		return nil
	}
	out := make([]ports.SubClientRule, len(j))
	copy(out, j)
	return out
}

func jsonSubRulesFromDomain(rules []ports.SubClientRule) jsonSubRules {
	if rules == nil {
		return nil
	}
	out := make(jsonSubRules, len(rules))
	copy(out, rules)
	return out
}

func (j jsonSubRules) Value() (driver.Value, error) {
	if j == nil {
		return "[]", nil
	}
	b, err := json.Marshal(j)
	return string(b), err
}

func (j *jsonSubRules) Scan(value any) error {
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
		return fmt.Errorf("unsupported scan type for jsonSubRules: %T", value)
	}
	if len(b) == 0 {
		*j = nil
		return nil
	}
	return json.Unmarshal(b, j)
}

// jsonSubImportClients is a JSON wrapper for []ports.SubImportClient.
type jsonSubImportClients []ports.SubImportClient

func (j jsonSubImportClients) toDomain() []ports.SubImportClient {
	if j == nil {
		return nil
	}
	out := make([]ports.SubImportClient, len(j))
	copy(out, j)
	return out
}

func jsonSubImportClientsFromDomain(clients []ports.SubImportClient) jsonSubImportClients {
	if clients == nil {
		return nil
	}
	out := make(jsonSubImportClients, len(clients))
	copy(out, clients)
	return out
}

func (j jsonSubImportClients) Value() (driver.Value, error) {
	if j == nil {
		return "[]", nil
	}
	b, err := json.Marshal(j)
	return string(b), err
}

func (j *jsonSubImportClients) Scan(value any) error {
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
		return fmt.Errorf("unsupported scan type for jsonSubImportClients: %T", value)
	}
	if len(b) == 0 {
		*j = nil
		return nil
	}
	return json.Unmarshal(b, j)
}

// jsonQuickLinks is a JSON wrapper for []ports.QuickLink.
type jsonQuickLinks []ports.QuickLink

func (j jsonQuickLinks) toDomain() []ports.QuickLink {
	if j == nil {
		return nil
	}
	out := make([]ports.QuickLink, len(j))
	copy(out, j)
	return out
}

func jsonQuickLinksFromDomain(links []ports.QuickLink) jsonQuickLinks {
	if links == nil {
		return nil
	}
	out := make(jsonQuickLinks, len(links))
	copy(out, links)
	return out
}

func (j jsonQuickLinks) Value() (driver.Value, error) {
	if j == nil {
		return "[]", nil
	}
	b, err := json.Marshal(j)
	return string(b), err
}

func (j *jsonQuickLinks) Scan(value any) error {
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
		return fmt.Errorf("unsupported scan type for jsonQuickLinks: %T", value)
	}
	if len(b) == 0 {
		*j = nil
		return nil
	}
	return json.Unmarshal(b, j)
}

// jsonGlobalAnnouncement is a JSON wrapper for ports.GlobalAnnouncement.
type jsonGlobalAnnouncement ports.GlobalAnnouncement

func (j jsonGlobalAnnouncement) toDomain() ports.GlobalAnnouncement {
	return ports.GlobalAnnouncement(j)
}

func jsonGlobalAnnouncementFromDomain(a ports.GlobalAnnouncement) jsonGlobalAnnouncement {
	return jsonGlobalAnnouncement(a)
}

func (j jsonGlobalAnnouncement) Value() (driver.Value, error) {
	b, err := json.Marshal(ports.GlobalAnnouncement(j))
	return string(b), err
}

func (j *jsonGlobalAnnouncement) Scan(value any) error {
	if value == nil {
		*j = jsonGlobalAnnouncement{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("unsupported scan type for jsonGlobalAnnouncement: %T", value)
	}
	if len(b) == 0 {
		*j = jsonGlobalAnnouncement{}
		return nil
	}
	return json.Unmarshal(b, j)
}

func (uiSettingsRow) TableName() string { return "ui_settings" }

type ruleSetRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	Slug      string `gorm:"size:128;uniqueIndex;not null"`
	Name      string `gorm:"size:255"`
	Sort      int
	Enabled   bool   `gorm:"default:true"`
	Content   string `gorm:"type:text"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (ruleSetRow) TableName() string { return "rule_sets" }

type mailSettingsRow struct {
	ID                   int64 `gorm:"primaryKey"`
	Enabled              bool
	SMTPHost             string `gorm:"size:255"`
	SMTPPort             int
	SMTPUsername         string `gorm:"size:255"`
	SMTPPassword         string `gorm:"type:text"`
	FromEmail            string `gorm:"size:255"`
	FromName             string `gorm:"size:128"`
	Encryption           string `gorm:"size:16"`
	ExpireBeforeDays     int
	TrafficRemainPercent int
	UpdatedAt            time.Time
}

func (mailSettingsRow) TableName() string { return "mail_settings" }

func (r *mailSettingsRow) toDomain() (domain.MailSettings, error) {
	password, err := decryptSecret(r.SMTPPassword)
	if err != nil {
		return domain.MailSettings{}, err
	}
	return domain.MailSettings{
		Enabled:              r.Enabled,
		SMTPHost:             r.SMTPHost,
		SMTPPort:             r.SMTPPort,
		SMTPUsername:         r.SMTPUsername,
		SMTPPassword:         password,
		FromEmail:            r.FromEmail,
		FromName:             r.FromName,
		Encryption:           r.Encryption,
		ExpireBeforeDays:     r.ExpireBeforeDays,
		TrafficRemainPercent: r.TrafficRemainPercent,
	}, nil
}

func mailSettingsFromDomain(s domain.MailSettings) (*mailSettingsRow, error) {
	password, err := encryptSecret(s.SMTPPassword)
	if err != nil {
		return nil, err
	}
	return &mailSettingsRow{
		ID:                   1,
		Enabled:              s.Enabled,
		SMTPHost:             s.SMTPHost,
		SMTPPort:             s.SMTPPort,
		SMTPUsername:         s.SMTPUsername,
		SMTPPassword:         password,
		FromEmail:            s.FromEmail,
		FromName:             s.FromName,
		Encryption:           s.Encryption,
		ExpireBeforeDays:     s.ExpireBeforeDays,
		TrafficRemainPercent: s.TrafficRemainPercent,
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

// ---- JSON field wrappers (driver.Valuer / sql.Scanner) ----

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
// saml_config / oidc_config table. Same shape as jsonStrings — Value()
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
		&auditRow{},
		&subLogRow{},
		&syncTaskRow{},
		&xuiPanelRow{},
		&uiSettingsRow{},
		&ruleSetRow{},
		&mailSettingsRow{},
		&mailTemplateRow{},
		&mailSentRow{},
		&samlConfigRow{},
		&oidcConfigRow{},
	); err != nil {
		return err
	}
	return backfillTrafficCounterNulls(db)
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
