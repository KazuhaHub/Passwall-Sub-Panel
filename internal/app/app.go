// Package app is the dependency-injection composition root. It assembles
// the adapter, service and transport layers into one ready-to-serve
// application. main.go is intentionally tiny — all wiring lives here.
package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/mysql"
	xuiadapter "github.com/KazuhaHub/passwall-sub-panel/internal/adapters/xui"
	yamladapter "github.com/KazuhaHub/passwall-sub-panel/internal/adapters/yaml"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
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
)

// App holds everything assembled for serving. Run() blocks on
// ListenAndServe and runs the background workers in goroutines; Shutdown
// cancels both.
type App struct {
	cfg       *config.Config
	server    *http.Server
	traffic   *traffic.Service
	reconcile *reconcile.Service
	saml      *auth.SAMLService

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
	if err := mysql.Migrate(db); err != nil {
		return nil, fmt.Errorf("db migrate: %w", err)
	}
	mysqlRepos := mysql.NewRepos(db)

	ruleSetRepo, err := yamladapter.NewRuleSetRepo(cfg.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("rule_set repo: %w", err)
	}
	templateRepo, err := yamladapter.NewTemplateRepo(cfg.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("template repo: %w", err)
	}
	secretKey := []byte(os.Getenv("PSP_SECRET_KEY"))
	xuiPanelRepo, err := yamladapter.NewXUIPanelRepo(cfg.XUIPanelsFile, secretKey)
	if err != nil {
		return nil, fmt.Errorf("xui_panels repo: %w", err)
	}
	samlCfg, err := yamladapter.LoadSAMLConfig(cfg.SAMLFile)
	if err != nil {
		return nil, fmt.Errorf("saml config: %w", err)
	}

	repos := ports.Repos{
		User:      mysqlRepos.User,
		Group:     mysqlRepos.Group,
		Node:      mysqlRepos.Node,
		Ownership: mysqlRepos.Ownership,
		Traffic:   mysqlRepos.Traffic,
		Audit:     mysqlRepos.Audit,
		SubLog:    mysqlRepos.SubLog,
		RuleSet:   ruleSetRepo,
		Template:  templateRepo,
		XUIPanel:  xuiPanelRepo,
	}

	pool, err := xuiadapter.NewPool(ctx, repos.XUIPanel)
	if err != nil {
		return nil, fmt.Errorf("xui pool: %w", err)
	}

	// --- service layer ---
	issuer := jwtutil.NewIssuer(
		cfg.JWTSecret,
		cfg.AccessTTL(),
		cfg.RefreshTTL(),
		cfg.JWT.Issuer,
	)
	authSvc := auth.New(issuer)
	samlSvc, err := auth.NewSAML(samlCfg)
	if err != nil {
		return nil, fmt.Errorf("init saml: %w", err)
	}
	auditSvc := audit.New(repos.Audit)
	groupSvc := group.New(repos.Group, repos.Node)
	syncSvc := syncsvc.New(pool, repos.Ownership)
	userSvc := user.New(repos.User, repos.Group, repos.Ownership, groupSvc, syncSvc, pool)
	nodeSvc := node.New(repos.Node, pool, syncSvc)
	trafficSvc := traffic.New(repos.User, repos.Traffic, pool, userSvc)
	reconcileSvc := reconcile.New(repos.User, repos.Ownership, repos.Node, repos.Audit, pool, syncSvc)
	renderSvc := render.New(repos, pool, groupSvc)

	_ = auditSvc // direct AuditSvc wiring lands when admin handlers start recording diffs

	// --- transport layer ---
	httpHandler := httptransport.NewRouter(httptransport.Deps{
		Cfg:     cfg,
		Repos:   repos,
		Pool:    pool,
		Auth:    authSvc,
		SAML:    samlSvc,
		User:    userSvc,
		Group:   groupSvc,
		Node:    nodeSvc,
		Render:  renderSvc,
		Audit:   auditSvc,
		Sync:    syncSvc,
		Traffic: trafficSvc,
	})

	return &App{
		cfg:       cfg,
		traffic:   trafficSvc,
		reconcile: reconcileSvc,
		saml:      samlSvc,
		server: &http.Server{
			Addr:              cfg.Listen,
			Handler:           httpHandler,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}, nil
}

// Run launches background workers (SAML metadata refresh, traffic poll,
// reconciliation) and then blocks on ListenAndServe.
func (a *App) Run() error {
	bgCtx, cancel := context.WithCancel(context.Background())
	a.bgCancel = cancel

	a.saml.StartMetadataRefresh(bgCtx)
	go a.runTrafficLoop(bgCtx)
	go a.runReconcileLoop(bgCtx)

	return a.server.ListenAndServe()
}

// Shutdown stops background workers and gracefully closes the HTTP server.
func (a *App) Shutdown(ctx context.Context) error {
	if a.bgCancel != nil {
		a.bgCancel()
	}
	return a.server.Shutdown(ctx)
}

func (a *App) runTrafficLoop(ctx context.Context) {
	interval := a.cfg.TrafficPullInterval()
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
	interval := a.cfg.ReconcileInterval()
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
