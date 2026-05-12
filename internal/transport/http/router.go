// Package http wires up the HTTP transport layer.
package http

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/audit"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	syncsvc "github.com/KazuhaHub/passwall-sub-panel/internal/service/sync"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/traffic"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/handler"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// Deps bundles every dependency the HTTP layer needs. App-startup wiring
// populates this and passes it to NewRouter.
type Deps struct {
	Cfg     *config.Config
	Repos   ports.Repos
	Pool    ports.XUIPool
	Auth    *auth.Service
	SAML    *auth.SAMLService
	User    *user.Service
	Group   *group.Service
	Node    *node.Service
	Render  *render.Service
	Audit   *audit.Service
	Sync    *syncsvc.Service
	Traffic *traffic.Service
}

// NewRouter returns a configured *gin.Engine ready to be served.
func NewRouter(d Deps) *gin.Engine {
	g := gin.New()
	g.Use(gin.Logger(), gin.Recovery())

	// Public endpoints
	g.GET("/health", handler.Health)

	subLimiter := middleware.NewPerIPLimiter(d.Cfg.RateLimit.SubPerIPPerMin, time.Minute)
	subHandler := handler.NewSubHandler(d.User, d.Render, d.Repos.SubLog)
	g.GET("/sub/:token", subLimiter.Handler(), subHandler.Get)

	// Auth endpoints
	authLocal := handler.NewAuthLocalHandler(d.Auth, d.User)
	loginLimiter := middleware.NewPerIPLimiter(d.Cfg.RateLimit.LoginPerIPPerMin, time.Minute)
	authGroup := g.Group("/api/auth")
	{
		authGroup.POST("/local/login", loginLimiter.Handler(), authLocal.Login)
		if d.SAML != nil && d.SAML.Enabled() {
			samlHandler := handler.NewAuthSAMLHandler(d.SAML, d.Auth, d.User, d.Cfg)
			authGroup.GET("/saml/login", samlHandler.Login)
			authGroup.POST("/saml/acs", samlHandler.ACS)
			authGroup.GET("/saml/metadata", samlHandler.Metadata)
		}
	}

	// Authenticated user self-service
	userMe := handler.NewUserMeHandler(d.User, d.Traffic, d.Cfg)
	userGroup := g.Group("/api/user/me",
		middleware.RequireAuth(d.Auth),
		middleware.RequireRole(domain.RoleUser, domain.RoleAdmin),
	)
	{
		userGroup.GET("", userMe.Profile)
		userGroup.GET("/traffic", userMe.Traffic)
		userGroup.POST("/reset-sub-token", userMe.ResetSubToken)
		userGroup.POST("/change-password", userMe.ChangePassword)
	}

	// Admin API
	adminGroup := g.Group("/api/admin",
		middleware.RequireAuth(d.Auth),
		middleware.RequireRole(domain.RoleAdmin),
	)
	{
		users := handler.NewAdminUserHandler(d.User, d.Cfg)
		adminGroup.GET("/users", users.List)
		adminGroup.POST("/users", users.Create)
		adminGroup.GET("/users/:id", users.Get)
		adminGroup.PUT("/users/:id", users.Update)
		adminGroup.DELETE("/users/:id", users.Delete)
		adminGroup.POST("/users/:id/reset-sub-token", users.ResetSubToken)
		adminGroup.POST("/users/:id/reset-uuid", users.ResetUUID)
		adminGroup.POST("/users/:id/set-enabled", users.SetEnabled)

		nodes := handler.NewAdminNodeHandler(d.Node, d.Sync, d.Repos.Ownership)
		adminGroup.GET("/nodes", nodes.List)
		adminGroup.GET("/nodes/:id", nodes.Get)
		adminGroup.POST("/nodes/import", nodes.ImportExisting)
		adminGroup.POST("/nodes", nodes.CreateInbound)
		adminGroup.PUT("/nodes/:id/metadata", nodes.UpdateMetadata)
		adminGroup.PUT("/nodes/:id/inbound", nodes.UpdateInboundConfig)
		adminGroup.POST("/nodes/:id/set-enabled", nodes.SetEnabled)
		adminGroup.DELETE("/nodes/:id", nodes.Delete)
		adminGroup.GET("/nodes/unmanaged", nodes.ListUnmanaged)
		adminGroup.POST("/nodes/:id/claim", nodes.ClaimClient)

		groups := handler.NewAdminGroupHandler(d.Group, d.User, d.Repos.User)
		adminGroup.GET("/groups", groups.List)
		adminGroup.GET("/groups/:id", groups.Get)
		adminGroup.POST("/groups", groups.Create)
		adminGroup.PUT("/groups/:id", groups.Update)
		adminGroup.PUT("/groups/:id/layout", groups.UpdateLayout)
		adminGroup.DELETE("/groups/:id", groups.Delete)

		rules := handler.NewAdminRuleSetsHandler(d.Repos.RuleSet)
		adminGroup.GET("/rules", rules.List)
		adminGroup.GET("/rules/:slug", rules.Get)
		adminGroup.PUT("/rules/:slug", rules.Save)
		adminGroup.DELETE("/rules/:slug", rules.Delete)

		templates := handler.NewAdminTemplatesHandler(d.Repos.Template)
		adminGroup.GET("/templates", templates.List)
		adminGroup.GET("/templates/:slug", templates.Get)
		adminGroup.PUT("/templates/:slug", templates.Save)
		adminGroup.DELETE("/templates/:slug", templates.Delete)

		audit := handler.NewAdminAuditHandler(d.Repos.Audit)
		adminGroup.GET("/audit", audit.List)

		trafficH := handler.NewAdminTrafficHandler(d.Repos.User, d.Traffic)
		adminGroup.GET("/traffic/top", trafficH.Top)
		adminGroup.GET("/traffic/user/:id", trafficH.UserReport)
	}

	// Static SPA bundle (embedded). Must be registered last so /api and
	// /sub keep precedence.
	g.NoRoute(handler.StaticSPA)

	return g
}
