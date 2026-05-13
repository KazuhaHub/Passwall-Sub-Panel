package mysql

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ---- GORM row types ----
//
// Each row type mirrors a domain entity 1:1 but carries GORM tags and JSON
// wrappers. The service layer only ever touches domain.* types; rows are an
// adapter-internal concern, converted via to/from helpers.

type userRow struct {
	ID                 int64   `gorm:"primaryKey;autoIncrement"`
	Username           string  `gorm:"size:64;uniqueIndex;not null"`
	UPN                *string `gorm:"size:255;uniqueIndex"`
	Source             string  `gorm:"size:16;not null"`
	PasswordHash       string  `gorm:"size:255"`
	Role               string  `gorm:"size:16;not null;default:user"`
	SubToken           string  `gorm:"size:64;uniqueIndex;not null"`
	UUID               string  `gorm:"size:36;not null"`
	GroupID            int64   `gorm:"index;not null"`
	EnabledRuleSets    jsonStrings
	PersonalRules      string `gorm:"type:text"`
	ExpireAt           *time.Time
	TrafficLimitBytes  int64
	TrafficResetPeriod string `gorm:"size:16;default:never"`
	TrafficPeriodStart *time.Time
	DisplayName        string `gorm:"size:128"`
	Remark             string `gorm:"size:255"`
	Enabled            bool   `gorm:"not null"`
	AutoDisabledReason string `gorm:"size:32"`
	EmergencyUsedCount int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (userRow) TableName() string { return "users" }

func (r *userRow) toDomain() *domain.User {
	upn := ""
	if r.UPN != nil {
		upn = *r.UPN
	}
	return &domain.User{
		ID:                 r.ID,
		Username:           r.Username,
		UPN:                upn,
		Source:             domain.UserSource(r.Source),
		PasswordHash:       r.PasswordHash,
		Role:               domain.Role(r.Role),
		SubToken:           r.SubToken,
		UUID:               r.UUID,
		GroupID:            r.GroupID,
		EnabledRuleSets:    []string(r.EnabledRuleSets),
		PersonalRules:      r.PersonalRules,
		ExpireAt:           r.ExpireAt,
		TrafficLimitBytes:  r.TrafficLimitBytes,
		TrafficResetPeriod: domain.ResetPeriod(r.TrafficResetPeriod),
		TrafficPeriodStart: r.TrafficPeriodStart,
		DisplayName:        r.DisplayName,
		Remark:             r.Remark,
		Enabled:            r.Enabled,
		AutoDisabledReason: domain.AutoDisabledReason(r.AutoDisabledReason),
		EmergencyUsedCount: r.EmergencyUsedCount,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
}

func userFromDomain(u *domain.User) *userRow {
	var upn *string
	if u.UPN != "" {
		upn = &u.UPN
	}
	return &userRow{
		ID:                 u.ID,
		Username:           u.Username,
		UPN:                upn,
		Source:             string(u.Source),
		PasswordHash:       u.PasswordHash,
		Role:               string(u.Role),
		SubToken:           u.SubToken,
		UUID:               u.UUID,
		GroupID:            u.GroupID,
		EnabledRuleSets:    jsonStrings(u.EnabledRuleSets),
		PersonalRules:      u.PersonalRules,
		ExpireAt:           u.ExpireAt,
		TrafficLimitBytes:  u.TrafficLimitBytes,
		TrafficResetPeriod: string(u.TrafficResetPeriod),
		TrafficPeriodStart: u.TrafficPeriodStart,
		DisplayName:        u.DisplayName,
		Remark:             u.Remark,
		Enabled:            u.Enabled,
		AutoDisabledReason: string(u.AutoDisabledReason),
		EmergencyUsedCount: u.EmergencyUsedCount,
		CreatedAt:          u.CreatedAt,
		UpdatedAt:          u.UpdatedAt,
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
	ID            int64  `gorm:"primaryKey;autoIncrement"`
	PanelID       int64  `gorm:"not null;index;uniqueIndex:uk_panel_inbound,priority:1"`
	PanelName     string `gorm:"size:64;index"`
	InboundID     int    `gorm:"not null;uniqueIndex:uk_panel_inbound,priority:2"`
	DisplayName   string `gorm:"size:255;not null"`
	ServerAddress string `gorm:"size:255"`
	Region        string `gorm:"size:16;not null"`
	Tags          jsonStrings
	SortOrder     int  `gorm:"default:0"`
	Enabled       bool `gorm:"default:true"`
	CreatedAt     time.Time
}

func (nodeRow) TableName() string { return "nodes" }

func (r *nodeRow) toDomain() *domain.Node {
	return &domain.Node{
		ID:            r.ID,
		PanelID:       r.PanelID,
		PanelName:     r.PanelName,
		InboundID:     r.InboundID,
		DisplayName:   r.DisplayName,
		ServerAddress: r.ServerAddress,
		Region:        r.Region,
		Tags:          []string(r.Tags),
		SortOrder:     r.SortOrder,
		Enabled:       r.Enabled,
		CreatedAt:     r.CreatedAt,
	}
}

func nodeFromDomain(n *domain.Node) *nodeRow {
	return &nodeRow{
		ID:            n.ID,
		PanelID:       n.PanelID,
		PanelName:     n.PanelName,
		InboundID:     n.InboundID,
		DisplayName:   n.DisplayName,
		ServerAddress: n.ServerAddress,
		Region:        n.Region,
		Tags:          jsonStrings(n.Tags),
		SortOrder:     n.SortOrder,
		Enabled:       n.Enabled,
		CreatedAt:     n.CreatedAt,
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
	LogoURL            string `gorm:"size:1024"`
	LogoURLDark        string `gorm:"size:1024"`
	EmailDomain        string `gorm:"size:255"`
	AuditRetentionDays int
	SubBaseURL         string `gorm:"size:512"`
	// Runtime tuning (restart required to take effect).
	CronTrafficPullMinutes     int
	CronReconcileMinutes       int
	JWTAccessTTLMinutes        int
	JWTRefreshTTLMinutes       int
	JWTIssuer                  string `gorm:"size:128"`
	SubPerIPPerMin             int
	LoginPerIPPerMin           int
	SyncTaskRetentionDays      int
	DisallowUserLocalLogin     bool
	DisallowUserPasswordChange bool
	EmergencyAccessEnabled     bool
	EmergencyAccessHours       int
	EmergencyAccessMaxCount    int
	UpdatedAt                  time.Time
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

func (xuiPanelRow) TableName() string { return "xui_panels" }

func (r *xuiPanelRow) toDomain() *domain.XUIPanel {
	return &domain.XUIPanel{
		ID:       r.ID,
		Name:     r.Name,
		URL:      r.URL,
		APIToken: r.APIToken,
		Username: r.Username,
		Password: r.Password,
		Remark:   r.Remark,
	}
}

func xuiPanelFromDomain(p *domain.XUIPanel) *xuiPanelRow {
	return &xuiPanelRow{
		ID:       p.ID,
		Name:     p.Name,
		URL:      p.URL,
		APIToken: p.APIToken,
		Username: p.Username,
		Password: p.Password,
		Remark:   p.Remark,
	}
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
	return db.AutoMigrate(
		&userRow{},
		&groupRow{},
		&nodeRow{},
		&ownershipRow{},
		&trafficRow{},
		&auditRow{},
		&subLogRow{},
		&syncTaskRow{},
		&xuiPanelRow{},
		&uiSettingsRow{},
		&ruleSetRow{},
		&samlConfigRow{},
		&oidcConfigRow{},
	)
}

// wrapNotFound maps GORM's ErrRecordNotFound to domain.ErrNotFound so that
// service-layer code can rely on a single sentinel.
func wrapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ErrNotFound
	}
	return err
}
