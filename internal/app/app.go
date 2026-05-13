// Package app is the dependency-injection composition root. It assembles
// the adapter, service and transport layers into one ready-to-serve
// application. main.go is intentionally tiny — all wiring lives here.
package app

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/mysql"
	xuiadapter "github.com/KazuhaHub/passwall-sub-panel/internal/adapters/xui"
	yamladapter "github.com/KazuhaHub/passwall-sub-panel/internal/adapters/yaml"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
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
	httptransport "github.com/KazuhaHub/passwall-sub-panel/internal/transport/http"
	"golang.org/x/crypto/bcrypt"
)

// App holds everything assembled for serving. Run() blocks on
// ListenAndServe and runs the background workers in goroutines; Shutdown
// cancels both.
type App struct {
	cfg       *config.Config
	server    *http.Server
	traffic   *traffic.Service
	reconcile *reconcile.Service
	user      *user.Service
	node      *node.Service
	audit     *audit.Service
	settings  ports.SettingsRepo
	syncTasks ports.SyncTaskRepo
	saml      *auth.SAMLService

	// Resolved at startup from the settings DB. Loops/handlers using these
	// see "restart required to change" semantics for the underlying setting.
	trafficInterval   time.Duration
	reconcileInterval time.Duration

	bgCancel context.CancelFunc
}

// Build assembles the App from the loaded Config. It does NOT start any
// goroutines or listeners; call Run() for that.
func Build(ctx context.Context, cfg *config.Config) (*App, error) {
	// --- adapter layer ---
	db, err := mysql.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	if err := mysql.EnsureSchema(db); err != nil {
		return nil, fmt.Errorf("db schema: %w", err)
	}
	mysqlRepos := mysql.NewRepos(db)
	if err := mysqlRepos.SyncTask.ResetRunning(ctx); err != nil {
		return nil, fmt.Errorf("reset sync tasks: %w", err)
	}
	if err := syncPanelNameCaches(ctx, mysqlRepos); err != nil {
		return nil, fmt.Errorf("sync server name caches: %w", err)
	}

	templateRepo, err := yamladapter.NewTemplateRepo(cfg.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("template repo: %w", err)
	}

	samlCfg, err := mysqlRepos.SAMLConfig.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load saml config: %w", err)
	}

	oidcCfg, err := mysqlRepos.OIDCConfig.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load oidc config: %w", err)
	}

	repos := ports.Repos{
		User:       mysqlRepos.User,
		Group:      mysqlRepos.Group,
		Node:       mysqlRepos.Node,
		Ownership:  mysqlRepos.Ownership,
		Traffic:    mysqlRepos.Traffic,
		Audit:      mysqlRepos.Audit,
		SubLog:     mysqlRepos.SubLog,
		SyncTask:   mysqlRepos.SyncTask,
		RuleSet:    mysqlRepos.RuleSet,
		Template:   templateRepo,
		XUIPanel:   mysqlRepos.XUIPanel,
		Settings:   mysqlRepos.Settings,
		SAMLConfig: mysqlRepos.SAMLConfig,
		OIDCConfig: mysqlRepos.OIDCConfig,
	}

	if err := initAdminIfNeeded(ctx, repos); err != nil {
		return nil, fmt.Errorf("init admin: %w", err)
	}

	pool, err := xuiadapter.NewPool(ctx, repos.XUIPanel)
	if err != nil {
		return nil, fmt.Errorf("xui pool: %w", err)
	}

	// --- service layer ---
	// Cron intervals + rate limits are still startup-loaded (the tickers /
	// middleware they configure aren't rebuilt mid-run). JWT TTLs and the
	// "iss" claim ARE live: the issuer reads them from settings on every
	// IssueAccess / IssueRefresh.
	sysSettings, err := repos.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	issuer := jwtutil.NewIssuer(cfg.JWTSecret, func() jwtutil.Params {
		s, err := repos.Settings.Load(context.Background(), ports.UISettings{})
		if err != nil {
			// Settings load failed (DB blip?). Fall back to the hardcoded
			// defaults baked into settings_repo so logins keep working.
			return jwtutil.Params{
				AccessTTL:  120 * time.Minute,
				RefreshTTL: 7 * 24 * time.Hour,
				Issuer:     "passwall-sub-panel",
			}
		}
		return jwtutil.Params{
			AccessTTL:  time.Duration(s.JWTAccessTTLMinutes) * time.Minute,
			RefreshTTL: time.Duration(s.JWTRefreshTTLMinutes) * time.Minute,
			Issuer:     s.JWTIssuer,
		}
	})
	authSvc := auth.New(issuer)
	samlSvc, err := auth.NewSAML(samlCfg)
	if err != nil {
		return nil, fmt.Errorf("init saml: %w", err)
	}
	oidcSvc, err := auth.NewOIDC(oidcCfg)
	if err != nil {
		return nil, fmt.Errorf("init oidc: %w", err)
	}
	auditSvc := audit.New(repos.Audit)
	groupSvc := group.New(repos.Group, repos.Node)
	syncSvc := syncsvc.New(pool, repos.Ownership)
	userSvc := user.New(repos.User, repos.Group, repos.Ownership, repos.SyncTask, groupSvc, syncSvc, pool, repos.Settings)
	nodeSvc := node.New(repos.Node, pool, syncSvc, repos.SyncTask, repos.Group, repos.User, syncSvc, repos.Settings)
	trafficSvc := traffic.New(repos.User, repos.Ownership, repos.Traffic, pool, userSvc)
	reconcileSvc := reconcile.New(repos.User, repos.Ownership, repos.Node, repos.Group, repos.Settings, repos.Audit, pool, syncSvc)
	renderSvc := render.New(repos, pool, groupSvc)

	// --- transport layer ---
	httpHandler := httptransport.NewRouter(httptransport.Deps{
		Cfg:              cfg,
		Repos:            repos,
		Pool:             pool,
		Auth:             authSvc,
		SAML:             samlSvc,
		OIDC:             oidcSvc,
		User:             userSvc,
		Group:            groupSvc,
		Node:             nodeSvc,
		Render:           renderSvc,
		Audit:            auditSvc,
		Sync:             syncSvc,
		Traffic:          trafficSvc,
		Reconcile:        reconcileSvc,
		SubPerIPPerMin:   sysSettings.SubPerIPPerMin,
		LoginPerIPPerMin: sysSettings.LoginPerIPPerMin,
	})

	return &App{
		cfg:               cfg,
		traffic:           trafficSvc,
		reconcile:         reconcileSvc,
		user:              userSvc,
		node:              nodeSvc,
		audit:             auditSvc,
		settings:          repos.Settings,
		syncTasks:         repos.SyncTask,
		saml:              samlSvc,
		trafficInterval:   time.Duration(sysSettings.CronTrafficPullMinutes) * time.Minute,
		reconcileInterval: time.Duration(sysSettings.CronReconcileMinutes) * time.Minute,
		server: &http.Server{
			Addr:              cfg.Listen,
			Handler:           httpHandler,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}, nil
}

func syncPanelNameCaches(ctx context.Context, repos ports.Repos) error {
	panels, err := repos.XUIPanel.List(ctx)
	if err != nil {
		return err
	}
	for _, panel := range panels {
		if err := repos.Node.UpdatePanelName(ctx, panel.ID, panel.Name); err != nil {
			return err
		}
		if err := repos.Ownership.UpdatePanelName(ctx, panel.ID, panel.Name); err != nil {
			return err
		}
	}
	return nil
}

// Run launches background workers (SAML metadata refresh, traffic poll,
// reconciliation) and then blocks on ListenAndServe.
func (a *App) Run() error {
	bgCtx, cancel := context.WithCancel(context.Background())
	a.bgCancel = cancel

	a.saml.StartMetadataRefresh(bgCtx)
	go a.runSyncTaskLoop(bgCtx)
	go a.runAuditCleanupLoop(bgCtx)
	go a.runTrafficLoop(bgCtx)
	go a.runReconcileLoop(bgCtx)

	return a.server.ListenAndServe()
}

func (a *App) runAuditCleanupLoop(ctx context.Context) {
	interval := time.Hour
	t := time.NewTicker(interval)
	defer t.Stop()
	log.Info("audit cleanup loop started", "interval", interval.String())
	for {
		a.pruneAudit(ctx)
		a.pruneSyncTasks(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *App) pruneSyncTasks(ctx context.Context) {
	if a.settings == nil || a.syncTasks == nil {
		return
	}
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return
	}
	if settings.SyncTaskRetentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -settings.SyncTaskRetentionDays)
	deleted, err := a.syncTasks.DeleteSucceededBefore(ctx, cutoff)
	if err != nil {
		log.Warn("sync task cleanup", "err", err)
		return
	}
	if deleted > 0 {
		log.Info("sync task cleanup", "deleted", deleted, "retention_days", settings.SyncTaskRetentionDays)
	}
}

func (a *App) pruneAudit(ctx context.Context) {
	if a.audit == nil || a.settings == nil {
		return
	}
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		log.Warn("audit cleanup load settings", "err", err)
		return
	}
	if settings.AuditRetentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -settings.AuditRetentionDays)
	deleted, err := a.audit.PruneBefore(ctx, cutoff)
	if err != nil {
		log.Warn("audit cleanup", "err", err)
		return
	}
	if deleted > 0 {
		log.Info("audit cleanup", "deleted", deleted, "retention_days", settings.AuditRetentionDays)
	}
}

func (a *App) runSyncTaskLoop(ctx context.Context) {
	interval := 30 * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	log.Info("sync task loop started", "interval", interval.String())
	for {
		if err := a.user.ProcessDueTasks(ctx, 20); err != nil {
			log.Warn("user sync tasks", "err", err)
		}
		if err := a.node.ProcessDueTasks(ctx, 20); err != nil {
			log.Warn("node sync tasks", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Shutdown stops background workers and gracefully closes the HTTP server.
func (a *App) Shutdown(ctx context.Context) error {
	if a.bgCancel != nil {
		a.bgCancel()
	}
	return a.server.Shutdown(ctx)
}

func (a *App) runTrafficLoop(ctx context.Context) {
	interval := a.trafficInterval
	t := time.NewTicker(interval)
	defer t.Stop()
	log.Info("traffic loop started", "interval", interval.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := a.traffic.PollOnce(ctx); err != nil {
				log.Warn("traffic poll", "err", err)
			}
		}
	}
}

func (a *App) runReconcileLoop(ctx context.Context) {
	interval := a.reconcileInterval
	t := time.NewTicker(interval)
	defer t.Stop()
	log.Info("reconcile loop started", "interval", interval.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			report, err := a.reconcile.RunOnce(ctx, reconcile.LevelFull)
			if err != nil {
				log.Warn("reconcile run", "err", err)
				continue
			}
			if report.Scanned > 0 || len(report.Issues) > 0 {
				log.Info("reconcile pass",
					"scanned", report.Scanned, "fixed", report.Fixed, "issues", len(report.Issues))
			}
		}
	}
}

func initAdminIfNeeded(ctx context.Context, repos ports.Repos) error {
	roleAdmin := domain.RoleAdmin
	_, total, err := repos.User.List(ctx, ports.UserFilter{
		Role:       &roleAdmin,
		Pagination: ports.Pagination{Page: 1, PageSize: 1},
	})
	if err != nil {
		return err
	}
	if total > 0 {
		return nil
	}

	g, err := repos.Group.GetBySlug(ctx, "default")
	if err != nil {
		g = &domain.Group{
			Slug:      "default",
			Name:      "Default",
			TagFilter: domain.TagFilter{All: true},
		}
		if err := repos.Group.Create(ctx, g); err != nil {
			return err
		}
	}

	pw, err := idgen.NewPassword()
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	subToken, err := idgen.NewSubToken()
	if err != nil {
		return err
	}
	now := time.Now()
	u := &domain.User{
		Username:           "admin",
		PasswordHash:       string(hash),
		Role:               domain.RoleAdmin,
		SubToken:           subToken,
		UUID:               idgen.NewUUID(),
		GroupID:            g.ID,
		TrafficResetPeriod: domain.ResetMonthly,
		TrafficPeriodStart: &now,
		Enabled:            true,
		Remark:             "bootstrap admin",
	}
	if err := repos.User.Create(ctx, u); err != nil {
		return err
	}

	log.Info("bootstrap admin created",
		"username", u.Username,
		"password", pw,
		"user_id", u.ID,
		"group", g.Slug)
	return nil
}
