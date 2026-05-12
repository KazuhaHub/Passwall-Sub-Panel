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
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*domain.Node, error)
	GetByPanelInbound(ctx context.Context, panel string, inboundID int) (*domain.Node, error)
	List(ctx context.Context) ([]*domain.Node, error)
	ListEnabled(ctx context.Context) ([]*domain.Node, error)
}

type OwnershipRepo interface {
	Add(ctx context.Context, e *domain.XUIClientEntry) error
	Remove(ctx context.Context, id int64) error
	RemoveByMatch(ctx context.Context, panel string, inboundID int, email string) error
	GetByMatch(ctx context.Context, panel string, inboundID int, email string) (*domain.XUIClientEntry, error)
	ListByUser(ctx context.Context, userID int64) ([]*domain.XUIClientEntry, error)
	ListByInbound(ctx context.Context, panel string, inboundID int) ([]*domain.XUIClientEntry, error)
	Exists(ctx context.Context, panel string, inboundID int, email string) (bool, error)
	// UpdateUUID rewrites client_uuid for the row identified by the unique
	// (panel, inbound, email) triple. Used by the UUID-rotation flow so the
	// ownership table tracks the same uuid that's now in 3X-UI.
	UpdateUUID(ctx context.Context, panel string, inboundID int, email, newUUID string) error
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
}

type SubLogRepo interface {
	Insert(ctx context.Context, log *domain.SubLog) error
	List(ctx context.Context, filter SubLogFilter) (items []*domain.SubLog, total int64, err error)
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
	GetByName(ctx context.Context, name string) (*domain.XUIPanel, error)
}

// Repos aggregates all repository ports for dependency injection.
type Repos struct {
	User      UserRepo
	Group     GroupRepo
	Node      NodeRepo
	Ownership OwnershipRepo
	Traffic   TrafficRepo
	Audit     AuditRepo
	SubLog    SubLogRepo
	RuleSet   RuleSetRepo
	Template  TemplateRepo
	XUIPanel  XUIPanelRepo
}
