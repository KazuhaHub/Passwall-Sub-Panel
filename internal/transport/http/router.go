// Package http wires up the HTTP transport layer.
package http

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/audit"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/reconcile"
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
	Cfg       *config.Config
	Repos     ports.Repos
	Pool      ports.XUIPool
	Auth      *auth.Service
	SAML      *auth.SAMLService
	OIDC      *auth.OIDCService
	User      *user.Service
	Group     *group.Service
	Node      *node.Service
	Render    *render.Service
	Audit     *audit.Service
	Sync      *syncsvc.Service
	Traffic   *traffic.Service
	Reconcile *reconcile.Service

	// Rate-limit caps resolved from the DB settings table at startup. The
	// middleware uses fixed buckets so admin edits require a restart for
	// these to refresh. JWT TTLs are NOT here — the issuer reads them
	// live via auth.Service.AccessTTL/RefreshTTL.
	SubPerIPPerMin   int
	LoginPerIPPerMin int
	JWTParams        *jwtutil.ParamsCache
}

// NewRouter returns a configured *gin.Engine ready to be served.
func NewRouter(d Deps) *gin.Engine {
	g := gin.New()
	g.Use(gin.Logger(), gin.Recovery())

	// Public endpoints
	g.GET("/health", handler.Health)

	subLimiter := middleware.NewPerIPLimiter(d.SubPerIPPerMin, time.Minute)
	subHandler := handler.NewSubHandler(d.User, d.Render, d.Repos.SubLog)
	g.GET("/sub/:token", subLimiter.Handler(), subHandler.Get)

	// Auth endpoints
	authLocal := handler.NewAuthLocalHandler(d.Auth, d.User, d.SAML, d.OIDC, d.Repos.Settings)
	loginLimiter := middleware.NewPerIPLimiter(d.LoginPerIPPerMin, time.Minute)
	authGroup := g.Group("/api/auth")
	{
		authGroup.GET("/methods", authLocal.Methods)
		authGroup.POST("/local/login", loginLimiter.Handler(), authLocal.Login)
		// SAML endpoints stay registered even when SSO is currently
		// disabled — the underlying service rejects calls until admin
		// re-enables it. That way an admin who flips SSO on in the panel
		// doesn't need a restart for the routes to appear.
		samlHandler := handler.NewAuthSAMLHandler(d.SAML, d.Auth, d.User)
		authGroup.GET("/saml/login", samlHandler.Login)
		authGroup.POST("/saml/acs", samlHandler.ACS)
		authGroup.GET("/saml/metadata", samlHandler.Metadata)

		oidcHandler := handler.NewAuthOIDCHandler(d.OIDC, d.Auth, d.User)
		authGroup.GET("/oidc/login", oidcHandler.Login)
		authGroup.GET("/oidc/callback", oidcHandler.Callback)

		ssoComplete := handler.NewAuthSSOCompleteHandler(d.Auth, d.User)
		authGroup.GET("/sso-complete", ssoComplete.Complete)
	}

	// Authenticated user self-service
	userMe := handler.NewUserMeHandler(d.User, d.Traffic, d.Repos.Settings)
	userGroup := g.Group("/api/user/me",
		middleware.RequireAuth(d.Auth, d.User),
		middleware.RequireRole(domain.RoleUser, domain.RoleAdmin),
	)
	{
		userGroup.GET("", userMe.Profile)
		userGroup.GET("/traffic", userMe.Traffic)
		userGroup.POST("/emergency-access", userMe.EmergencyAccess)
		userGroup.POST("/reset-credentials", userMe.ResetCredentials)
		userGroup.POST("/change-password", userMe.ChangePassword)
	}

	// Admin API
	adminGroup := g.Group("/api/admin",
		middleware.RequireAuth(d.Auth, d.User),
		middleware.RequireRole(domain.RoleAdmin),
	)
	adminGroup.Use(middleware.AdminAudit(d.Audit))
	{
		users := handler.NewAdminUserHandler(d.User, d.Repos.Settings)
		adminGroup.GET("/users", users.List)
		adminGroup.POST("/users", users.Create)
		adminGroup.GET("/users/:id", users.Get)
		adminGroup.PUT("/users/:id", users.Update)
		adminGroup.DELETE("/users/:id", users.Delete)
		adminGroup.POST("/users/:id/reset-credentials", users.ResetCredentials)
		adminGroup.POST("/users/:id/reset-emergency-usage", users.ResetEmergencyUsage)
		adminGroup.POST("/users/:id/set-enabled", users.SetEnabled)

		nodes := handler.NewAdminNodeHandler(d.Node, d.Sync, d.Repos.Ownership, d.Repos.XUIPanel)
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
		adminGroup.POST("/nodes/generate-reality-keypair", nodes.GenerateRealityKeypair)

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

		auditH := handler.NewAdminAuditHandler(d.Repos.Audit)
		adminGroup.GET("/audit", auditH.List)
		adminGroup.DELETE("/audit", auditH.Clear)

		trafficH := handler.NewAdminTrafficHandler(d.Repos.User, d.Traffic)
		adminGroup.GET("/traffic/top", trafficH.Top)
		adminGroup.GET("/traffic/user/:id", trafficH.UserReport)
		adminGroup.PUT("/traffic/user/:id", trafficH.SetUserUsage)

		servers := handler.NewAdminServersHandler(d.Repos.XUIPanel, d.Pool, d.Repos.Node, d.Repos.Ownership)
		adminGroup.GET("/servers", servers.List)
		adminGroup.POST("/servers", servers.Create)
		adminGroup.PUT("/servers/:id", servers.Update)
		adminGroup.DELETE("/servers/:id", servers.Delete)
		adminGroup.POST("/servers/probe", servers.Test)

		settings := handler.NewAdminSettingsHandler(d.Repos.Settings, d.JWTParams)
		adminGroup.GET("/settings/ui", settings.Get)
		adminGroup.PUT("/settings/ui", settings.Put)

		samlAdmin := handler.NewAdminSAMLHandler(d.Repos.SAMLConfig, d.SAML, d.Repos.OIDCConfig, d.OIDC, d.Repos.Settings)
		adminGroup.GET("/settings/saml", samlAdmin.Get)
		adminGroup.PUT("/settings/saml", samlAdmin.Put)

		oidcAdmin := handler.NewAdminOIDCHandler(d.Repos.OIDCConfig, d.OIDC, d.Repos.SAMLConfig, d.SAML)
		adminGroup.GET("/settings/oidc", oidcAdmin.Get)
		adminGroup.PUT("/settings/oidc", oidcAdmin.Put)

		recon := handler.NewAdminReconcileHandler(d.Reconcile)
		adminGroup.POST("/reconcile/run", recon.Run)

		tasks := handler.NewAdminSyncTasksHandler(d.Repos.SyncTask)
		adminGroup.GET("/sync-tasks", tasks.List)
		adminGroup.POST("/sync-tasks/:id/retry", tasks.Retry)
		adminGroup.POST("/sync-tasks/:id/cancel", tasks.Cancel)
		adminGroup.POST("/sync-tasks/purge", tasks.PurgeFinished)
	}

	// Static SPA bundle (embedded). Must be registered last so /api and
	// /sub keep precedence.
	g.NoRoute(handler.StaticSPA)

	return g
}
