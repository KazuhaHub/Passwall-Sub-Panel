// Package ports defines the abstract interfaces that service-layer code
// depends on (the "ports" in hexagonal architecture).
//
// The service layer imports only this package; the concrete implementations
// live in adapters/{mysql,yaml,xui}. This separation keeps business logic
// decoupled from storage choices and external systems, and makes services
// trivially mockable in unit tests.
package ports

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ---- Common filter types ----

type Pagination struct {
	Page     int
	PageSize int
}

type UserFilter struct {
	Pagination
	Search  string
	GroupID *int64
	Source  *domain.UserSource
	Role    *domain.Role
	Enabled *bool
}

type AuditFilter struct {
	Pagination
	Actor  string
	Action string
	Since  *time.Time
	Until  *time.Time
}

type SubLogFilter struct {
	Pagination
	UserID *int64
	Since  *time.Time
	Until  *time.Time
}

type SyncTaskFilter struct {
	Pagination
	Status *domain.SyncTaskStatus
	Type   *domain.SyncTaskType
}

// ---- Repository interfaces ----

type UserRepo interface {
	Create(ctx context.Context, u *domain.User) error
	Update(ctx context.Context, u *domain.User) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.User, error)
	GetByUsername(ctx context.Context, username string) (*domain.User, error)
	GetByUPN(ctx context.Context, upn string) (*domain.User, error)
	GetBySubToken(ctx context.Context, token string) (*domain.User, error)
	List(ctx context.Context, filter UserFilter) (items []*domain.User, total int64, err error)
	ListByGroup(ctx context.Context, groupID int64) ([]*domain.User, error)
}

type GroupRepo interface {
	Create(ctx context.Context, g *domain.Group) error
	Update(ctx context.Context, g *domain.Group) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.Group, error)
	GetBySlug(ctx context.Context, slug string) (*domain.Group, error)
	List(ctx context.Context) ([]*domain.Group, error)
	CountMembers(ctx context.Context, id int64) (int64, error)
}

type NodeRepo interface {
	Create(ctx context.Context, n *domain.Node) error
	Update(ctx context.Context, n *domain.Node) error
	UpdatePanelName(ctx context.Context, panelID int64, panelName string) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.Node, error)
	GetByPanelInbound(ctx context.Context, panelID int64, inboundID int) (*domain.Node, error)
	List(ctx context.Context) ([]*domain.Node, error)
	ListEnabled(ctx context.Context) ([]*domain.Node, error)
}

type OwnershipRepo interface {
	Add(ctx context.Context, e *domain.XUIClientEntry) error
	Remove(ctx context.Context, id int64) error
	RemoveByMatch(ctx context.Context, panelID int64, inboundID int, email string) error
	GetByMatch(ctx context.Context, panelID int64, inboundID int, email string) (*domain.XUIClientEntry, error)
	ListByUser(ctx context.Context, userID int64) ([]*domain.XUIClientEntry, error)
	ListByInbound(ctx context.Context, panelID int64, inboundID int) ([]*domain.XUIClientEntry, error)
	Exists(ctx context.Context, panelID int64, inboundID int, email string) (bool, error)
	// UpdateUUID rewrites client_uuid for the row identified by the unique
	// (panel_id, inbound, email) triple. Used by the UUID-rotation flow so the
	// ownership table tracks the same uuid that's now in 3X-UI.
	UpdateUUID(ctx context.Context, panelID int64, inboundID int, email, newUUID string) error
	UpdatePanelName(ctx context.Context, panelID int64, panelName string) error
}

type TrafficRepo interface {
	Insert(ctx context.Context, s *domain.TrafficSnapshot) error
	LatestForUser(ctx context.Context, userID int64) (*domain.TrafficSnapshot, error)
	LastBefore(ctx context.Context, userID int64, before time.Time) (*domain.TrafficSnapshot, error)
	ListByUser(ctx context.Context, userID int64, since, until time.Time) ([]*domain.TrafficSnapshot, error)
}

type AuditRepo interface {
	Insert(ctx context.Context, e *domain.AuditEntry) error
	List(ctx context.Context, filter AuditFilter) (items []*domain.AuditEntry, total int64, err error)
	Clear(ctx context.Context) error
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

type SubLogRepo interface {
	Insert(ctx context.Context, log *domain.SubLog) error
	List(ctx context.Context, filter SubLogFilter) (items []*domain.SubLog, total int64, err error)
}

type SyncTaskRepo interface {
	Create(ctx context.Context, task *domain.SyncTask) error
	GetByID(ctx context.Context, id int64) (*domain.SyncTask, error)
	GetActiveByTarget(ctx context.Context, typ domain.SyncTaskType, targetType string, targetID int64) (*domain.SyncTask, error)
	// HasActiveByTargetAny reports whether any task of the given types is
	// pending/running for the target. Single round-trip alternative to
	// looping GetActiveByTarget per type.
	HasActiveByTargetAny(ctx context.Context, types []domain.SyncTaskType, targetType string, targetID int64) (bool, error)
	List(ctx context.Context, filter SyncTaskFilter) (items []*domain.SyncTask, total int64, err error)
	ListDue(ctx context.Context, now time.Time, limit int) ([]*domain.SyncTask, error)
	MarkRunning(ctx context.Context, id int64) error
	MarkSucceeded(ctx context.Context, id int64) error
	MarkRetry(ctx context.Context, id int64, lastError string, nextRunAt time.Time) error
	Cancel(ctx context.Context, id int64) error
	RetryNow(ctx context.Context, id int64) error
	ResetRunning(ctx context.Context) error
	// DeleteSucceededBefore prunes succeeded tasks finished before the cutoff.
	// Pending/running/canceled/retrying tasks are left alone — those are
	// either still working or worth keeping for forensic visibility.
	DeleteSucceededBefore(ctx context.Context, cutoff time.Time) (int64, error)
	// DeleteFinished wipes every non-active task (anything not pending or
	// running). Powers the admin's one-click "clear sync tasks" button.
	DeleteFinished(ctx context.Context) (int64, error)
}

type RuleSetRepo interface {
	List(ctx context.Context) ([]*domain.RuleSet, error)
	GetBySlug(ctx context.Context, slug string) (*domain.RuleSet, error)
	Save(ctx context.Context, r *domain.RuleSet) error
	Delete(ctx context.Context, slug string) error
}

type TemplateRepo interface {
	List(ctx context.Context) ([]*domain.Template, error)
	GetBySlug(ctx context.Context, slug string) (*domain.Template, error)
	GetDefault(ctx context.Context, clientType domain.ClientType) (*domain.Template, error)
	Save(ctx context.Context, t *domain.Template) error
	Delete(ctx context.Context, slug string) error
}

type XUIPanelRepo interface {
	List(ctx context.Context) ([]*domain.XUIPanel, error)
	GetByID(ctx context.Context, id int64) (*domain.XUIPanel, error)
	GetByName(ctx context.Context, name string) (*domain.XUIPanel, error)
	Save(ctx context.Context, panel *domain.XUIPanel) error
	Delete(ctx context.Context, id int64) error
}

// UISettings holds runtime-editable UI preferences. They live in the DB so
// admin edits don't touch infrastructure fields.
type UISettings struct {
	LoginMode string `yaml:"login_mode" json:"login_mode"`
	// SiteTitle is the brand name displayed in the sidebar, header, and
	// login page. Defaults to "Passwall".
	SiteTitle string `yaml:"site_title" json:"site_title"`
	// LogoURL is a URL to a custom logo image for light backgrounds.
	// When empty, the bundled default logo is used.
	LogoURL string `yaml:"logo_url" json:"logo_url"`
	// LogoURLDark is a URL to a custom logo for dark backgrounds.
	// Falls back to LogoURL if empty.
	LogoURLDark string `yaml:"logo_url_dark" json:"logo_url_dark"`
	// EmailDomain is the suffix used when building the 3X-UI client.email
	// for every panel user (local + SSO, no distinction). Format is
	// "<username>@<domain>". Defaults to "psp.local".
	EmailDomain string `yaml:"email_domain" json:"email_domain"`
	// AuditRetentionDays controls automatic audit cleanup. 0 means never
	// delete audit entries automatically.
	AuditRetentionDays int `yaml:"audit_retention_days" json:"audit_retention_days"`
	// SubBaseURL is the panel's public base URL used to render absolute
	// subscription URLs ("<base>/sub/<token>"). Empty falls back to relative
	// paths.
	SubBaseURL string `yaml:"sub_base_url" json:"sub_base_url"`

	// ---- Runtime tuning (restart required for changes to take effect) ----
	// Background cron intervals; minutes. 0 keeps the previous default.
	CronTrafficPullMinutes int `json:"cron_traffic_pull_minutes"`
	CronReconcileMinutes   int `json:"cron_reconcile_minutes"`

	// JWT signing parameters. AccessTTL/RefreshTTL in minutes; Issuer is the
	// "iss" claim.
	JWTAccessTTLMinutes  int    `json:"jwt_access_ttl_minutes"`
	JWTRefreshTTLMinutes int    `json:"jwt_refresh_ttl_minutes"`
	JWTIssuer            string `json:"jwt_issuer"`

	// Per-IP rate limits, requests per minute.
	SubPerIPPerMin   int `json:"sub_per_ip_per_min"`
	LoginPerIPPerMin int `json:"login_per_ip_per_min"`

	// Sync-task retention in days. Only "succeeded" tasks get auto-pruned;
	// pending/running stay (still doing work) and canceled/error stay
	// (worth keeping for diagnosis). 0 disables auto-cleanup.
	SyncTaskRetentionDays int `json:"sync_task_retention_days"`
}

type SettingsRepo interface {
	Load(ctx context.Context, defaults UISettings) (UISettings, error)
	Save(ctx context.Context, s UISettings) error
}

// SAMLConfigRepo persists the panel's SAML/SSO configuration in the
// relational DB so admin can edit it from the panel without shell access.
type SAMLConfigRepo interface {
	// Load returns the current SAML config, or default disabled config when
	// no row has been persisted yet.
	Load(ctx context.Context) (*config.SAMLConfig, error)
	Save(ctx context.Context, c *config.SAMLConfig) error
}

// OIDCConfigRepo persists the panel's OIDC/OAuth2 SSO configuration.
// Same single-row pattern as SAMLConfigRepo.
type OIDCConfigRepo interface {
	Load(ctx context.Context) (*config.OIDCConfig, error)
	Save(ctx context.Context, c *config.OIDCConfig) error
}

// Repos aggregates all repository ports for dependency injection.
type Repos struct {
	User       UserRepo
	Group      GroupRepo
	Node       NodeRepo
	Ownership  OwnershipRepo
	Traffic    TrafficRepo
	Audit      AuditRepo
	SubLog     SubLogRepo
	SyncTask   SyncTaskRepo
	RuleSet    RuleSetRepo
	Template   TemplateRepo
	XUIPanel   XUIPanelRepo
	Settings   SettingsRepo
	SAMLConfig SAMLConfigRepo
	OIDCConfig OIDCConfigRepo
}
