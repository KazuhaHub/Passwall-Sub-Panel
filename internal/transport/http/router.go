// Package http wires up the HTTP transport layer.
package http

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/alert"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/audit"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/authpolicy"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/captcha"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/cert"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/geo"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/login2fa"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/loginguard"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/mailer"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/passkey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/reconcile"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/recovery"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/registration"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	syncsvc "github.com/KazuhaHub/passwall-sub-panel/internal/service/sync"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/traffic"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/twofa"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/handler"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// AsyncDispatcher launches handler-spawned background work under the
// panel's lifecycle: ctx fires Done when the panel is shutting down so
// post-response goroutines stop reaching for cancelled DB / SMTP / 3X-UI
// calls, and the WaitGroup behind Go() lets App.Shutdown drain in-flight
// work before the process exits. The implementation lives in
// internal/app to avoid a dependency cycle.
type AsyncDispatcher interface {
	// Context returns the panel's background context. It is cancelled
	// at the start of Shutdown.
	Context() context.Context
	// Go runs fn in a new goroutine tracked by the panel's WaitGroup
	// and shielded by safego.Recover. name is used as the log label.
	Go(name string, fn func(ctx context.Context))
}

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
	Cert      *cert.Service
	Render    *render.Service
	Audit     *audit.Service
	Sync      *syncsvc.Service
	Traffic   *traffic.Service
	Mail      *mailer.Service
	Reconcile *reconcile.Service
	Geo       *geo.Service
	Async     AsyncDispatcher

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
	tp := trustedProxies(d.Cfg.HTTP.TrustedProxies)
	if err := g.SetTrustedProxies(tp); err != nil {
		panic("invalid trusted proxies: " + err.Error())
	}
	// WARN when the trust list is wide-open: every proxy header
	// (CF-Connecting-IP, X-Forwarded-For, etc.) is honored from any
	// source, so a direct connection can spoof a client IP and bypass
	// per-IP rate limiters. Only safe when the listen port isn't
	// publicly reachable (Docker network, UNIX socket, etc.).
	for _, cidr := range tp {
		if cidr == "0.0.0.0/0" || cidr == "::/0" {
			log.Warn("trusted_proxies trusts all sources — proxy headers (CF-Connecting-IP / X-Forwarded-For) can be spoofed by direct connections; lock to your proxy/CDN IP ranges or set trusted_proxies=none when listening directly")
			break
		}
	}
	// Real client IP discovery — zero-config defaults that work behind any
	// common reverse proxy without admin tuning. Order matters: CDN-specific
	// single-IP headers come first so they win over the standard XFF chain.
	// CAVEAT: under a wide-open trusted_proxies list (the zero-config default),
	// Gin treats every direct connection as a trusted proxy, so an attacker
	// connecting directly can forge any of these headers and fully control the
	// resolved ClientIP — rate-limit / login-lockout / audit IP keys are
	// spoofable in that mode (hence the WARN above). Lock trusted_proxies to
	// your real proxy/CDN ranges (or set it to "none" when listening directly)
	// for a trustworthy client IP. See the trustedProxies doc below.
	g.RemoteIPHeaders = []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"}
	g.Use(gin.Logger(), gin.Recovery())
	// Security headers (HSTS / X-Frame-Options / X-Content-Type-Options /
	// Referrer-Policy / CSP). Mounted early so every later handler — SPA
	// fallback, SAML metadata, sub render — picks them up by default.
	g.Use(middleware.SecurityHeaders())
	// Stash the trusted-proxy decision into each request's context so handler
	// helpers holding only an *http.Request (sub-URL inference, SAML SP entity
	// URLs) can refuse to honour an attacker-supplied X-Forwarded-Host.
	g.Use(middleware.ProxyTrust())
	// 1 MiB body cap. Covers every admin write + the typical SAMLResponse
	// (which is ~80 KiB). Audit middleware later does io.ReadAll(body) —
	// without this cap that's a memory-exhaustion vector.
	g.Use(middleware.BodyLimit(1 << 20))
	// Audit middleware lives at the engine level so it covers admin
	// endpoints AND the login attempt AND user self-service writes. The
	// path/method filter inside the middleware short-circuits cheaply for
	// everything else (static assets, sub fetches, health probes).
	// Dispatch the audit INSERT off the request thread when we have an
	// async dispatcher wired (production). Test paths that build a
	// router without one fall through to the synchronous legacy path.
	var auditDispatch middleware.AsyncDispatch
	if d.Async != nil {
		auditDispatch = d.Async.Go
	}
	g.Use(middleware.AuditWrites(d.Audit, auditDispatch))

	// Public endpoints
	g.GET("/health", handler.Health)
	g.GET("/api/version", handler.Version)

	// Subscription handler — uses dynamic path from settings.
	// The actual route is registered via NoRoute handler for dynamic path support.
	subHandler := handler.NewSubHandler(d.User, d.Render, d.Repos.SubLog, d.Repos.ScopedSettings, d.Repos.User, d.Mail, d.Async)
	subLimiter := middleware.NewPerIPLimiter(d.SubPerIPPerMin, time.Minute)
	subLimiter.SetLimitFunc(newSettingsIntCache(d.Repos.Settings, d.SubPerIPPerMin, func(s ports.UISettings) int { return s.SubPerIPPerMin }).get)
	subPathCache := newSubPathCache(d.Repos.Settings)

	// 2FA (TOTP) service — shared by the login challenge, the user self-service
	// enrollment endpoints, and the admin break-glass reset.
	twofaSvc := twofa.New(twofa.Deps{
		Users:    d.Repos.User,
		Settings: d.Repos.ScopedSettings,
		// So disabling TOTP keeps the recovery codes when a passkey remains as the
		// account's second factor (see twofa.clearTOTPKeepingFactors).
		PasskeyCount: func(ctx context.Context, userID int64) (int, error) {
			creds, err := d.Repos.WebAuthn.FindByUserID(ctx, userID)
			return len(creds), err
		},
	})

	// Passkey (WebAuthn) service — shared by the usernameless login endpoints and
	// the profile-page enrollment/management endpoints.
	passkeySvc := passkey.New(passkey.Deps{Creds: d.Repos.WebAuthn, Users: d.Repos.User, Settings: d.Repos.ScopedSettings})

	// Email-as-2FA service — emails a one-time login code as an alternative 2FA
	// factor (admin opt-in). Reuses the auth_tokens OTP machinery.
	login2faSvc := login2fa.New(login2fa.Deps{Tokens: d.Repos.AuthToken, Mail: d.Mail, Settings: d.Repos.ScopedSettings})

	// One shared captcha service: the image-challenge store must be the same
	// instance that issues (/auth/captcha) and verifies (login/register/forgot).
	captchaSvc := captcha.NewService()

	// Auth endpoints
	authLocal := handler.NewAuthLocalHandler(d.Auth, d.User, d.SAML, d.OIDC, d.Repos.ScopedSettings, d.Repos.AuthEvent,
		loginguard.New(d.Repos.AuthEvent), captchaSvc, twofaSvc, passkeySvc, login2faSvc)
	loginLimiter := middleware.NewPerIPLimiter(d.LoginPerIPPerMin, time.Minute)
	loginLimiter.SetLimitFunc(newSettingsIntCache(d.Repos.Settings, d.LoginPerIPPerMin, func(s ports.UISettings) int { return s.LoginPerIPPerMin }).get)
	authGroup := g.Group("/api/auth")
	{
		authGroup.GET("/methods", authLocal.Methods)
		// Image-captcha challenge for the login form. Shares the login limiter
		// so it can't be hammered to churn the captcha store.
		authGroup.GET("/captcha", loginLimiter.Handler(), authLocal.Captcha)
		authGroup.POST("/local/login", loginLimiter.Handler(), authLocal.Login)
		// Second factor of a 2FA login — exchanges the pending token + code for a
		// real session. Behind the login limiter so the code can't be brute-forced.
		authGroup.POST("/2fa/verify", loginLimiter.Handler(), authLocal.TwoFAVerify)
		// Alternative 2FA verification methods (v3.7.0): email one-time code and
		// passkey assertion. All share the login limiter.
		authGroup.POST("/2fa/email/send", loginLimiter.Handler(), authLocal.TwoFAEmailSend)
		authGroup.POST("/2fa/passkey/begin", loginLimiter.Handler(), authLocal.TwoFAPasskeyBegin)
		authGroup.POST("/2fa/passkey/finish", loginLimiter.Handler(), authLocal.TwoFAPasskeyFinish)
		// Usernameless (discoverable) passkey login. Behind the login limiter so
		// /begin isn't a free challenge-churn / DoS surface.
		passkeyAuth := handler.NewAuthPasskeyHandler(passkeySvc, d.Auth, d.Repos.Settings, d.Repos.AuthEvent)
		authGroup.POST("/passkey/begin", loginLimiter.Handler(), passkeyAuth.LoginBegin)
		authGroup.POST("/passkey/finish", loginLimiter.Handler(), passkeyAuth.LoginFinish)
		// Refresh shares the login limiter — refresh storms from a misbehaving
		// client should be throttled just like brute-force login attempts.
		authGroup.POST("/refresh", loginLimiter.Handler(), authLocal.Refresh)
		// Self-service password recovery (v3.7.0). Both share the login limiter:
		// forgot is the email-bombing throttle, reset is the token-guessing one.
		recoverySvc := recovery.New(recovery.Deps{
			Users:       d.Repos.User,
			Tokens:      d.Repos.AuthToken,
			Mail:        d.Mail,
			Settings:    d.Repos.Settings,
			SetPassword: d.User.SetPassword,
		})
		recoveryH := handler.NewAuthRecoveryHandler(recoverySvc, d.Repos.Settings, captchaSvc)
		authGroup.POST("/forgot-password", loginLimiter.Handler(), recoveryH.Forgot)
		authGroup.POST("/reset-password", loginLimiter.Handler(), recoveryH.Reset)
		// Self-service registration (v3.7.0). Email-verify reuses the auth_tokens
		// infrastructure; both endpoints share the login rate limiter.
		registerSvc := registration.New(registration.Deps{
			Users:    d.User,
			Groups:   d.Repos.Group,
			Tokens:   d.Repos.AuthToken,
			Mail:     d.Mail,
			Settings: d.Repos.Settings,
		})
		registerH := handler.NewAuthRegisterHandler(registerSvc, d.Repos.Settings, captchaSvc)
		authGroup.POST("/register", loginLimiter.Handler(), registerH.Register)
		authGroup.POST("/resend-verification", loginLimiter.Handler(), registerH.ResendVerification)
		authGroup.POST("/verify-email", loginLimiter.Handler(), registerH.VerifyEmail)
		// SAML endpoints stay registered even when SSO is currently
		// disabled — the underlying service rejects calls until admin
		// re-enables it. That way an admin who flips SSO on in the panel
		// doesn't need a restart for the routes to appear.
		samlHandler := handler.NewAuthSAMLHandler(d.SAML, d.Auth, d.User, d.Repos.AuthEvent)
		authGroup.GET("/saml/login", samlHandler.Login)
		authGroup.POST("/saml/acs", samlHandler.ACS)
		authGroup.GET("/saml/metadata", samlHandler.Metadata)

		oidcHandler := handler.NewAuthOIDCHandler(d.OIDC, d.Auth, d.User, d.Repos.AuthEvent)
		authGroup.GET("/oidc/login", oidcHandler.Login)
		authGroup.GET("/oidc/callback", oidcHandler.Callback)

		ssoComplete := handler.NewAuthSSOCompleteHandler(d.Auth, d.User)
		authGroup.GET("/sso-complete", ssoComplete.Complete)
	}

	// Authenticated user self-service
	// "Require 2FA" enforcement: decides whether an account must enroll a second
	// factor before using the panel, and the middleware that gates it. Shared by
	// the user, staff and admin trees + the /user/me profile flag.
	enroll2FA := authpolicy.New(authpolicy.Deps{Groups: d.Repos.Group, Passkeys: d.Repos.WebAuthn, Settings: d.Repos.ScopedSettings})
	require2FAGate := middleware.Require2FAEnrollment(enroll2FA, d.User)

	userMe := handler.NewUserMeHandler(d.User, d.Traffic, d.Repos.ScopedSettings, d.Repos.Node, d.Repos.Ownership, twofaSvc, passkeySvc, enroll2FA)
	userGroup := g.Group("/api/user/me",
		middleware.RequireAuth(d.Auth, d.User),
		// Operators are included so that an operator forced to enroll 2FA (via the
		// staff-wide / group / per-user requirement) can actually reach the
		// self-service enrollment ceremonies + /user/me — otherwise the gate below
		// would 403 them at RequireRole and lock them out with no way to enroll.
		// Per-handler logic already scopes what these endpoints do.
		middleware.RequireRole(domain.RoleUser, domain.RoleOperator, domain.RoleAdmin),
		require2FAGate,
	)
	{
		userGroup.GET("", userMe.Profile)
		userGroup.GET("/traffic", userMe.Traffic)
		userGroup.GET("/traffic/history", userMe.TrafficHistory)
		userGroup.GET("/server-status", userMe.ServerStatus)
		userGroup.GET("/rules", userMe.GetRules)
		userGroup.PUT("/rules", userMe.PutRules)
		userGroup.POST("/emergency-access", userMe.EmergencyAccess)
		userGroup.POST("/reset-credentials", userMe.ResetCredentials)
		userGroup.POST("/change-password", userMe.ChangePassword)
		// 2FA (TOTP) self-service enrollment / disable.
		userGroup.POST("/2fa/begin", userMe.Begin2FA)
		userGroup.POST("/2fa/enable", userMe.Enable2FA)
		userGroup.POST("/2fa/disable", userMe.Disable2FA)
		// Recovery-code rotation is step-up gated (current code as proof) and
		// rate-limited like other credential endpoints.
		userGroup.POST("/2fa/recovery/regenerate", loginLimiter.Handler(), userMe.RegenerateRecovery2FA)
		// Passkey step-up: authorize disable-TOTP / regenerate-recovery with a
		// passkey assertion (for users who hold a passkey but not their TOTP code).
		userGroup.POST("/2fa/stepup/passkey/begin", loginLimiter.Handler(), userMe.StepUpPasskeyBegin)
		userGroup.POST("/2fa/stepup/passkey/finish", loginLimiter.Handler(), userMe.StepUpPasskeyFinish)
		// Passkey (WebAuthn) self-service enrollment / management.
		userGroup.GET("/passkeys", userMe.ListPasskeys)
		userGroup.POST("/passkeys/begin", userMe.BeginPasskeyEnroll)
		userGroup.POST("/passkeys/finish", userMe.FinishPasskeyEnroll)
		userGroup.PATCH("/passkeys/:id", userMe.RenamePasskey)
		userGroup.DELETE("/passkeys/:id", userMe.DeletePasskey)
	}

	// Admin API.
	//
	// The /api/admin/* tree splits into two role gates:
	//   • staffGroup   — admin OR operator. Day-to-day work that doesn't touch
	//                    integration credentials or system-wide config: user
	//                    CRUD, traffic, sync tasks, sub logs, audit read,
	//                    template/rule reads, etc.
	//   • adminGroup   — admin only. Anything that holds passwords / API
	//                    tokens / SSO secrets, can lock the panel out, or
	//                    rewrites global behavior: 3X-UI panel CRUD, system
	//                    settings, mail SMTP / templates, SAML/OIDC, rule
	//                    set + template writes, audit clear.
	// Both share the AuditWrites engine-level middleware.
	staffGroup := g.Group("/api/admin",
		middleware.RequireAuth(d.Auth, d.User),
		middleware.RequireRole(domain.RoleAdmin, domain.RoleOperator),
		require2FAGate,
	)
	adminGroup := g.Group("/api/admin",
		middleware.RequireAuth(d.Auth, d.User),
		middleware.RequireRole(domain.RoleAdmin),
		require2FAGate,
	)
	{
		users := handler.NewAdminUserHandler(d.User, d.Repos.Settings, d.Mail, d.Async, twofaSvc, passkeySvc)
		// User CRUD is the operator's bread and butter. Handler-level guard
		// in users.Update prevents operators from creating/promoting other
		// admins or modifying an existing admin's role.
		staffGroup.GET("/users", users.List)
		staffGroup.POST("/users", users.Create)
		staffGroup.GET("/users/:id", users.Get)
		staffGroup.PUT("/users/:id", users.Update)
		staffGroup.DELETE("/users/:id", users.Delete)
		staffGroup.POST("/users/:id/reset-credentials", users.ResetCredentials)
		staffGroup.POST("/users/:id/reset-password", users.ResetPassword)
		staffGroup.POST("/users/:id/reset-emergency-usage", users.ResetEmergencyUsage)
		staffGroup.POST("/users/:id/reset-2fa", users.Reset2FA)
		staffGroup.POST("/users/:id/2fa/recovery/regenerate", users.RegenerateUser2FARecovery)
		// Passkey management (v3.7.0). List is read-only metadata; the revoke
		// paths are break-glass for a lost/compromised authenticator.
		staffGroup.GET("/users/:id/passkeys", users.ListUserPasskeys)
		staffGroup.DELETE("/users/:id/passkeys", users.RevokeAllUserPasskeys)
		staffGroup.DELETE("/users/:id/passkeys/:pkid", users.RevokeUserPasskey)
		staffGroup.POST("/users/:id/unlink-sso", users.UnlinkSSO)
		staffGroup.POST("/users/:id/set-enabled", users.SetEnabled)
		staffGroup.GET("/users/:id/rules", users.GetRules)
		staffGroup.PUT("/users/:id/rules", users.PutRules)
		// v3.9.0 cutover Stage 0: one-shot psp_client backfill (admin-only;
		// DB-only, idempotent, nothing reads psp_client in production yet).

		nodes := handler.NewAdminNodeHandler(d.Node, d.Sync, d.Repos.Ownership, d.Repos.User, d.Repos.XUIPanel)
		// Reads + toggle-enabled are operator-safe; create/update/delete and
		// the import / claim flows touch 3X-UI panels directly, admin only.
		staffGroup.GET("/nodes", nodes.List)
		staffGroup.GET("/nodes/:id", nodes.Get)
		staffGroup.POST("/nodes/:id/set-enabled", nodes.SetEnabled)
		staffGroup.GET("/nodes/unmanaged", nodes.ListUnmanaged)
		adminGroup.POST("/nodes/import", nodes.ImportExisting)
		// Separator endpoints: dedicated table since v3.0.0-beta.7, but URL
		// path stays under /admin/nodes/separator so the existing front-end
		// router doesn't need to learn a new top-level prefix.
		staffGroup.GET("/nodes/separator", nodes.ListSeparators)
		adminGroup.POST("/nodes/separator", nodes.CreateSeparator)
		adminGroup.PUT("/nodes/separator/reorder", nodes.ReorderSeparators)
		adminGroup.PUT("/nodes/separator/:id", nodes.UpdateSeparator)
		adminGroup.DELETE("/nodes/separator/:id", nodes.DeleteSeparator)
		adminGroup.POST("/nodes", nodes.CreateInbound)
		adminGroup.PUT("/nodes/reorder", nodes.Reorder)
		adminGroup.PUT("/nodes/:id/metadata", nodes.UpdateMetadata)
		adminGroup.PUT("/nodes/:id/inbound", nodes.UpdateInboundConfig)
		adminGroup.DELETE("/nodes/:id", nodes.Delete)
		adminGroup.POST("/nodes/:id/detach", nodes.Detach)
		adminGroup.POST("/nodes/:id/claim", nodes.ClaimClient)
		adminGroup.POST("/nodes/generate-reality-keypair", nodes.GenerateRealityKeypair)

		// PSP-managed certificates + DNS credentials (v3.6.4). adminGroup only —
		// these touch ACME private keys and DNS provider secrets.
		certs := handler.NewAdminCertHandler(d.Cert)
		adminGroup.GET("/certs", certs.List)
		adminGroup.GET("/certs/:id", certs.Get)
		adminGroup.POST("/certs", certs.Create)
		adminGroup.PUT("/certs/:id", certs.Update)
		adminGroup.POST("/certs/:id/renew", certs.Renew)
		adminGroup.GET("/certs/:id/download", certs.Download)
		adminGroup.DELETE("/certs/:id", certs.Delete)
		adminGroup.GET("/cert-events", certs.ListEvents)
		adminGroup.GET("/dns-credentials", certs.ListDNSCreds)
		adminGroup.POST("/dns-credentials", certs.CreateDNSCred)
		adminGroup.PUT("/dns-credentials/:id", certs.UpdateDNSCred)
		adminGroup.DELETE("/dns-credentials/:id", certs.DeleteDNSCred)
		adminGroup.GET("/dns-providers", certs.ListProviders)
		// ACME CA accounts (multi-account: certs select which CA account to issue under).
		adminGroup.GET("/acme-accounts", certs.ListACMEAccounts)
		adminGroup.POST("/acme-accounts", certs.CreateACMEAccount)
		adminGroup.PUT("/acme-accounts/:id", certs.UpdateACMEAccount)
		adminGroup.DELETE("/acme-accounts/:id", certs.DeleteACMEAccount)
		adminGroup.GET("/acme-key-types", certs.ListKeyTypes)
		adminGroup.PUT("/nodes/:id/cert-source", certs.SetNodeCertSource)

		groups := handler.NewAdminGroupHandler(d.Group, d.User, d.Repos.User)
		// Group CRUD shapes who can see which nodes — admin-only structure.
		// Operators need to read groups to pick one when creating a user.
		staffGroup.GET("/groups", groups.List)
		staffGroup.GET("/groups/:id", groups.Get)
		adminGroup.POST("/groups", groups.Create)
		adminGroup.PUT("/groups/:id", groups.Update)
		adminGroup.PUT("/groups/:id/layout", groups.UpdateLayout)
		adminGroup.DELETE("/groups/:id", groups.Delete)

		// Per-group setting overrides (v3.8.0). Admin-only — these are policy
		// settings (the 2FA enrollment group in Phase 1). The frontend overlays
		// the returned `overrides` onto the global settings to render the
		// inherit / overridden state per field.
		scopeSettings := handler.NewAdminScopeSettingsHandler(d.Repos.Group, d.Repos.ScopeSettings)
		adminGroup.GET("/groups/:id/scope-settings", scopeSettings.Get)
		adminGroup.PUT("/groups/:id/scope-settings", scopeSettings.SetOverride)
		adminGroup.DELETE("/groups/:id/scope-settings/:type/:name", scopeSettings.DeleteOverride)

		rules := handler.NewAdminRuleSetsHandler(d.Repos.RuleSet, d.Cfg.ConfigDir)
		staffGroup.GET("/rules", rules.List)
		staffGroup.GET("/rules/:slug", rules.Get)
		adminGroup.PUT("/rules/:slug", rules.Save)
		adminGroup.DELETE("/rules/:slug", rules.Delete)
		adminGroup.POST("/rules/:slug/reset", rules.Reset)

		templates := handler.NewAdminTemplatesHandler(d.Repos.Template, d.Cfg.ConfigDir)
		staffGroup.GET("/templates", templates.List)
		staffGroup.GET("/templates/:slug", templates.Get)
		adminGroup.PUT("/templates/:slug", templates.Save)
		adminGroup.DELETE("/templates/:slug", templates.Delete)
		adminGroup.POST("/templates/:slug/reset", templates.Reset)

		auditH := handler.NewAdminAuditHandler(d.Repos.Audit, d.Geo)
		// Read so operators can review their own actions; only admin can
		// wipe history.
		staffGroup.GET("/audit", auditH.List)
		adminGroup.DELETE("/audit", auditH.Clear)

		// Authentication-event log (logins across every method) — staff-readable
		// for security review; region enriched via Geo at view time.
		authEventsH := handler.NewAdminAuthEventsHandler(d.Repos.AuthEvent, d.Geo)
		staffGroup.GET("/auth-events", authEventsH.List)

		dashboard := handler.NewAdminDashboardHandler(d.Repos.User, d.Repos.Node, d.Repos.Group, d.Repos.XUIPanel, d.Repos.Certificate)
		staffGroup.GET("/dashboard/summary", dashboard.Summary)

		// Unified notification center: a single derived-alert feed the top-bar
		// bell consumes. UpgradeFor reads version state already in memory (latest
		// 3X-UI tag + the cached tested range), so panel_upgrade makes zero new
		// GitHub calls; CheckXUI(latest)==Supported ensures we only nudge toward
		// upgrades PSP has actually verified.
		alertSvc := alert.New(alert.Deps{
			Nodes:    d.Repos.Node,
			Panels:   d.Repos.XUIPanel,
			Certs:    d.Repos.Certificate,
			Events:   d.Repos.AuthEvent,
			Settings: d.Repos.Settings,
			// Nudge a panel to upgrade ONLY when it's below PSP's tested ceiling
			// (max_tested_xui), and only TOWARD that ceiling — never chasing a newer
			// upstream release PSP hasn't verified. A panel at/above the ceiling
			// gets no upgrade nudge (above is handled by the "Untested" badge).
			UpgradeFor: func(current string) (string, bool) {
				return version.XUIUpgradeTarget(current)
			},
			// Self-update nudge: a newer STABLE PSP release than this build.
			PSPUpgrade: func() (string, string, bool) {
				return version.Version, version.LatestPSP(), version.IsPSPUpdateAvailable()
			},
		})
		staffGroup.GET("/alerts", handler.NewAdminAlertsHandler(alertSvc).List)

		trafficH := handler.NewAdminTrafficHandler(d.Repos.User, d.Repos.Node, d.Repos.XUIPanel, d.Traffic, d.Repos.Settings)
		staffGroup.GET("/traffic/top", trafficH.Top)
		staffGroup.POST("/traffic/poll", trafficH.Poll)
		staffGroup.GET("/traffic/history", trafficH.History)
		staffGroup.GET("/traffic/user/:id", trafficH.UserReport)
		staffGroup.GET("/traffic/user/:id/history", trafficH.UserHistory)
		staffGroup.GET("/traffic/user/:id/nodes", trafficH.UserNodeUsage)
		staffGroup.GET("/traffic/user/:id/servers", trafficH.UserServerUsage)
		staffGroup.PUT("/traffic/user/:id", trafficH.SetUserUsage)
		staffGroup.GET("/traffic/nodes/top", trafficH.NodesTop)
		staffGroup.GET("/traffic/nodes/history", trafficH.NodesHistory)

		servers := handler.NewAdminServersHandler(d.Repos.XUIPanel, d.Pool, d.Repos.Node, d.Repos.Ownership, d.Repos.Audit, d.Async)
		// 3X-UI panel credentials live here — never operator.
		adminGroup.GET("/servers", servers.List)
		adminGroup.POST("/servers", servers.Create)
		adminGroup.PUT("/servers/:id", servers.Update)
		adminGroup.DELETE("/servers/:id", servers.Delete)
		adminGroup.POST("/servers/probe", servers.Test)
		adminGroup.POST("/servers/:id/upgrade-panel", servers.UpgradePanel)
		adminGroup.POST("/servers/:id/upgrade-xray", servers.UpgradeXray)
		adminGroup.GET("/servers/:id/xray-versions", servers.ListXrayVersions)
		adminGroup.GET("/servers/:id/web-cert", servers.WebCert)

		settings := handler.NewAdminSettingsHandler(d.Repos.Settings, d.JWTParams)
		adminGroup.GET("/settings/ui", settings.Get)
		adminGroup.PUT("/settings/ui", settings.Put)

		// Offline geo database status + manual update (touches the update token
		// + fetches an external DB — admin only).
		geoip := handler.NewAdminGeoIPHandler(d.Geo)
		adminGroup.GET("/settings/geoip/status", geoip.Status)
		adminGroup.POST("/settings/geoip/update", geoip.Update)

		mail := handler.NewAdminMailHandler(d.Mail)
		adminGroup.GET("/settings/mail", mail.Get)
		adminGroup.PUT("/settings/mail", mail.PutSettings)
		adminGroup.PUT("/settings/mail/templates/:kind", mail.PutTemplate)
		adminGroup.POST("/settings/mail/templates/:kind/preview", mail.PreviewTemplate)
		adminGroup.POST("/settings/mail/templates/:kind/reset", mail.ResetTemplate)
		adminGroup.POST("/settings/mail/test", mail.Test)
		adminGroup.POST("/settings/mail/announcement", mail.Announcement)

		samlAdmin := handler.NewAdminSAMLHandler(d.Repos.SAMLConfig, d.SAML, d.Repos.Settings)
		adminGroup.GET("/settings/saml", samlAdmin.Get)
		adminGroup.PUT("/settings/saml", samlAdmin.Put)
		adminGroup.POST("/settings/saml/fetch", samlAdmin.FetchMetadata)

		oidcAdmin := handler.NewAdminOIDCHandler(d.Repos.OIDCConfig, d.OIDC)
		adminGroup.GET("/settings/oidc", oidcAdmin.Get)
		adminGroup.PUT("/settings/oidc", oidcAdmin.Put)

		recon := handler.NewAdminReconcileHandler(d.Reconcile)
		// Reconcile rewrites 3X-UI side state — admin only.
		adminGroup.POST("/reconcile/run", recon.Run)

		tasks := handler.NewAdminSyncTasksHandler(d.Repos.SyncTask)
		staffGroup.GET("/sync-tasks", tasks.List)
		staffGroup.POST("/sync-tasks/:id/retry", tasks.Retry)
		staffGroup.POST("/sync-tasks/:id/cancel", tasks.Cancel)
		adminGroup.POST("/sync-tasks/purge", tasks.PurgeFinished)

		subLogs := handler.NewAdminSubLogHandler(d.Repos.SubLog, d.Repos.Settings, d.Geo)
		staffGroup.GET("/sub-logs", subLogs.List)
		adminGroup.DELETE("/sub-logs", subLogs.Clear)
		adminGroup.POST("/sub-logs/purge", subLogs.Purge)

		emailLogs := handler.NewAdminEmailLogHandler(d.Repos.Mail, d.Repos.Settings)
		staffGroup.GET("/email-logs", emailLogs.List)
		adminGroup.DELETE("/email-logs", emailLogs.Clear)
		adminGroup.POST("/email-logs/purge", emailLogs.Purge)
	}

	// Static SPA bundle (embedded). Must be registered last so /api and
	// subscription path keep precedence.
	// NoRoute handles both dynamic subscription paths and SPA fallback.
	g.NoRoute(func(c *gin.Context) {
		// Check if this is a subscription request (cached, no DB query).
		if subPathCache.isSubRequest(c.Request.URL.Path) {
			subLimiter.Handler()(c)
			if !c.IsAborted() {
				subHandler.Get(c)
			}
			return
		}
		// Otherwise serve SPA.
		handler.StaticSPA(c)
	})

	return g
}

// trustedProxies resolves the SetTrustedProxies argument from the
// http.trusted_proxies config value (or PSP_TRUSTED_PROXIES env
// override, applied earlier during config load). The fail-secure
// default — empty / unset — trusts ONLY the loopback range. This
// matters because Gin honors CF-Connecting-IP / X-Real-IP /
// X-Forwarded-For from any trusted proxy when computing
// `c.ClientIP()`, and several middlewares key on that IP (per-IP
// sub-rate-limit, per-IP login-rate-limit, audit log IP column). A
// wide-open trust list lets a direct attacker spoof any header to
// bypass those limits.
//
// Admin tokens for this knob:
//
//	(unset)           → loopback only (127.0.0.1/32, ::1/128). Safe
//	                    default for direct listen; behind a proxy/CDN
//	                    every client looks like the proxy's IP — fix
//	                    by setting the value below.
//	"all" / "*"       → trust every source (the pre-v3.6.1-beta.2
//	                    default). Use only when the listen port is
//	                    NOT publicly reachable (Docker network, UNIX
//	                    socket, etc.) — direct connections cannot
//	                    spoof in that topology. Boot logs WARN when
//	                    this mode is active.
//	"none"            → disable the trust list entirely. Gin's
//	                    ClientIP returns the raw TCP peer; proxy
//	                    headers are ignored regardless of source.
//	"<cidr>[,<cidr>]" → trust only the listed networks. The
//	                    recommended production form: name your
//	                    Cloudflare/Caddy/Nginx IP ranges explicitly.
func trustedProxies(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		// Loopback-only: safe default when admin hasn't configured
		// anything. Catches the "exposed to internet without a proxy
		// in front" footgun the old default left wide-open.
		return []string{"127.0.0.1/32", "::1/128"}
	}
	if strings.EqualFold(raw, "all") || raw == "*" {
		return []string{"0.0.0.0/0", "::/0"}
	}
	if strings.EqualFold(raw, "none") {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// subPathCache caches the subscription path prefix to avoid DB queries on every request.
type subPathCache struct {
	mu          sync.RWMutex
	prefix      string
	nextRefresh time.Time
	repo        ports.SettingsRepo
}

func newSubPathCache(repo ports.SettingsRepo) *subPathCache {
	c := &subPathCache{repo: repo}
	c.refresh()
	return c
}

func (c *subPathCache) refresh() {
	s, err := c.repo.Load(context.Background(), ports.UISettings{SubPath: "sub"})
	prefix := "/sub/"
	subPath := strings.Trim(strings.TrimSpace(s.SubPath), "/")
	if err != nil || subPath == "" {
		prefix = "/sub/"
	} else {
		prefix = "/" + subPath + "/"
	}
	c.mu.Lock()
	c.prefix = prefix
	c.nextRefresh = time.Now().Add(5 * time.Second)
	c.mu.Unlock()
}

func (c *subPathCache) isSubRequest(path string) bool {
	c.mu.RLock()
	prefix := c.prefix
	stale := time.Now().After(c.nextRefresh)
	c.mu.RUnlock()
	if stale {
		c.refresh()
		c.mu.RLock()
		prefix = c.prefix
		c.mu.RUnlock()
	}
	return strings.HasPrefix(path, prefix)
}

// settingsIntCache caches one int-valued setting with a short TTL so a
// hot-reloadable rate limit (read on every request via PerIPLimiter.SetLimitFunc)
// doesn't query the DB each time. Mirrors subPathCache's refresh cadence. On a
// load failure or a non-positive value it returns fallback, so a settings outage
// never opens the rate-limit gate to unlimited requests.
type settingsIntCache struct {
	mu          sync.RWMutex
	val         int
	nextRefresh time.Time
	repo        ports.SettingsRepo
	pick        func(ports.UISettings) int
	fallback    int
}

func newSettingsIntCache(repo ports.SettingsRepo, fallback int, pick func(ports.UISettings) int) *settingsIntCache {
	c := &settingsIntCache{repo: repo, fallback: fallback, pick: pick}
	c.refresh()
	return c
}

func (c *settingsIntCache) refresh() {
	v := c.fallback
	if s, err := c.repo.Load(context.Background(), ports.UISettings{}); err == nil {
		if got := c.pick(s); got > 0 {
			v = got
		}
	}
	c.mu.Lock()
	c.val = v
	c.nextRefresh = time.Now().Add(5 * time.Second)
	c.mu.Unlock()
}

func (c *settingsIntCache) get() int {
	c.mu.RLock()
	v := c.val
	stale := time.Now().After(c.nextRefresh)
	c.mu.RUnlock()
	if stale {
		c.refresh()
		c.mu.RLock()
		v = c.val
		c.mu.RUnlock()
	}
	return v
}
