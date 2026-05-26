// Package app is the dependency-injection composition root. It assembles
// the adapter, service and transport layers into one ready-to-serve
// application. main.go is intentionally tiny — all wiring lives here.
package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/mysql"
	xuiadapter "github.com/KazuhaHub/passwall-sub-panel/internal/adapters/xui"
	yamladapter "github.com/KazuhaHub/passwall-sub-panel/internal/adapters/yaml"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"

	"github.com/KazuhaHub/passwall-sub-panel/internal/service/audit"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/health"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/mailer"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/reconcile"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/rollup"
	syncsvc "github.com/KazuhaHub/passwall-sub-panel/internal/service/sync"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/traffic"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	httptransport "github.com/KazuhaHub/passwall-sub-panel/internal/transport/http"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
	"golang.org/x/crypto/bcrypt"
)

// asyncDispatcher implements httptransport.AsyncDispatcher in terms of
// App's background context + WaitGroup. Constructed during Build so
// handlers receive the live channels at wiring time.
type asyncDispatcher struct {
	ctx context.Context
	wg  *sync.WaitGroup
}

func (a *asyncDispatcher) Context() context.Context { return a.ctx }

func (a *asyncDispatcher) Go(name string, fn func(ctx context.Context)) {
	if a == nil || fn == nil {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	safego.GoTracked(a.wg, name, func() { fn(ctx) })
}

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
	mail      *mailer.Service
	health    *health.Service
	settings  ports.SettingsRepo
	syncTasks ports.SyncTaskRepo
	// trafficRepo / nodeTraffic kept for the retention cron — PruneBefore is
	// outside traffic.Service's surface (it's a maintenance concern, not a
	// poll-cycle concern), so app.go reaches into the repos directly.
	trafficRepo ports.TrafficRepo
	nodeTraffic ports.NodeTrafficRepo
	rollup      *rollup.Service
	saml        *auth.SAMLService
	// repos kept around so Run() can call initAdminIfNeeded AFTER the
	// listen socket is bound — that way a bind failure (port busy / TLS
	// misconfig / EACCES) doesn't leave the user staring at a closed
	// terminal that just printed the bootstrap admin password.
	repos ports.Repos

	// Resolved at startup from the settings DB. Loops/handlers using these
	// see "restart required to change" semantics for the underlying setting.
	trafficInterval   time.Duration
	reconcileInterval time.Duration
	healthInterval    time.Duration

	bgCancel  context.CancelFunc
	bgRootCtx context.Context
	bgWG      sync.WaitGroup

	// compatProbeInflight guards probePanelVersionsOnce so two ticks
	// can't run the per-panel loop concurrently — if cycle N's probe
	// is still walking (e.g. one panel is timing out at 10s each), the
	// next traffic tick should skip enqueuing another rather than pile
	// up. Atomic-bool flip via CompareAndSwap; the loser logs Debug and
	// returns immediately.
	compatProbeInflight atomic.Bool

	// xuiPool kept so Run() can fire a one-shot boot version probe across
	// every configured 3X-UI panel — services already hold their own
	// pool reference for their hot paths, this field is just for the
	// app-level lifecycle hook.
	xuiPool ports.XUIPool
}

// Build assembles the App from the loaded Config. It does NOT start any
// goroutines or listeners; call Run() for that.
func Build(ctx context.Context, cfg *config.Config) (*App, error) {
	// Wire the version-compat on-disk cache to PSP's DataDir BEFORE any
	// RefreshRemoteCompat could fire, so the first refresh persists to
	// the right place and a same-process load picks up the cached
	// snapshot. LoadCompatCache here means a cold boot with no network
	// still has a sane active range to work from — admins won't be
	// stuck staring at "compat unknown" until the first manual Test.
	version.SetCacheDir(cfg.DataDir)
	if err := version.LoadCompatCache(); err != nil {
		log.Warn("load compat cache (will recover on first refresh)", "err", err)
	}
	// Same boot pattern for the centralized "latest 3X-UI release tag"
	// snapshot: cold-boot off the cache so the ⋮ kebab "update available"
	// badge can render immediately, then the first RefreshLatestXUI call
	// (boot probe / Servers Test piggyback) overwrites it from upstream.
	if err := version.LoadLatestXUICache(); err != nil {
		log.Warn("load latest-xui cache (will recover on first refresh)", "err", err)
	}

	// --- adapter layer ---
	db, err := mysql.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	if err := mysql.EnsureSchema(db); err != nil {
		return nil, fmt.Errorf("db schema: %w", err)
	}
	mysql.ConfigureSecretKey(cfg.SecretKeyMaterial())
	// Boot-time secrets audit: walk the rows that hold encrypted creds
	// and WARN if any are still plaintext. Catches the silent-downgrade
	// case where ConfigureSecretKey("") makes encryptSecret a no-op.
	mysql.AuditSecretsAtRest(db)
	mysqlRepos := mysql.NewRepos(db)
	if err := mysqlRepos.SyncTask.ResetRunning(ctx); err != nil {
		return nil, fmt.Errorf("reset sync tasks: %w", err)
	}
	// Note: pre-v3 we ran syncPanelNameCaches here to refresh the
	// (now-deleted) panel_name columns on nodes / user_xui_clients. v3
	// resolves panel names at render time from the in-memory pool, so
	// there's no cache to sync at startup.

	templateRepo, err := yamladapter.NewTemplateRepo(cfg.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("template repo: %w", err)
	}
	ruleSetRepo, err := yamladapter.NewRuleSetRepo(cfg.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("ruleset repo: %w", err)
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
		User:        mysqlRepos.User,
		Group:       mysqlRepos.Group,
		Node:        mysqlRepos.Node,
		Separator:   mysqlRepos.Separator,
		Ownership:   mysqlRepos.Ownership,
		Traffic:     mysqlRepos.Traffic,
		NodeTraffic: mysqlRepos.NodeTraffic,
		Audit:       mysqlRepos.Audit,
		SubLog:      mysqlRepos.SubLog,
		SyncTask:    mysqlRepos.SyncTask,
		RuleSet:     ruleSetRepo,
		Template:    templateRepo,
		XUIPanel:    mysqlRepos.XUIPanel,
		Settings:    mysqlRepos.Settings,
		Mail:        mysqlRepos.Mail,
		SAMLConfig:  mysqlRepos.SAMLConfig,
		OIDCConfig:  mysqlRepos.OIDCConfig,
	}

	// Admin bootstrap is deferred to Run() — see the comment on App.repos.
	// We must NOT print the initial password before knowing the listen
	// socket can actually bind; the user would lose it to a crash dump.

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
				// Keep in lockstep with settings_kv_repo.go defaults (60min / 1d).
				AccessTTL:  60 * time.Minute,
				RefreshTTL: 24 * time.Hour,
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
	nodeSvc := node.New(repos.Node, repos.Separator, pool, syncSvc, repos.SyncTask, repos.Group, repos.User, syncSvc, repos.Settings)
	trafficSvc := traffic.New(repos.User, repos.Ownership, repos.Traffic, repos.Node, repos.NodeTraffic, pool, userSvc).WithSettings(repos.Settings)
	// Wire the two-way dependency for the traffic-floor safety net: user
	// needs traffic to compute current-period usage; traffic needs user to
	// push the resulting floor into 3X-UI after each poll. Both fields are
	// nil-tolerant so the order here doesn't open a startup race window.
	userSvc.SetTrafficUsage(trafficSvc)
	trafficSvc.SetConfigPusher(userSvc)
	mailSvc := mailer.New(repos.Mail, repos.User, repos.Traffic, repos.Settings, repos.SyncTask)
	// Late-bind the mailer into the traffic poll so quota-exhaustion disables
	// and period-rollover re-enables actually email the user (the only path
	// that produces those notifications).
	trafficSvc.SetMailNotifier(mailSvc)
	reconcileSvc := reconcile.New(repos.User, repos.Ownership, repos.Node, repos.Group, repos.Settings, repos.Audit, pool, syncSvc)
	healthSvc := health.New(repos.Node)
	renderSvc := render.New(repos, pool, groupSvc)

	// --- async dispatcher for handler-spawned background work ---
	//
	// Built BEFORE the HTTP layer so handlers can capture the panel-wide
	// background context and WaitGroup at construction time. Cancel-side
	// is owned by Shutdown; the dispatcher only exposes the read-only
	// context plus a recover-safe Go() helper. The fields point into the
	// final *App allocated below — pre-allocating the context + WaitGroup
	// here keeps the dispatcher closure live the moment a handler is hit
	// (e.g. an admin POST after Build but before Run), and Shutdown will
	// fire bgCancel even if Run never started.
	bgCtx, cancel := context.WithCancel(context.Background())
	a := &App{
		cfg:       cfg,
		bgCancel:  cancel,
		bgRootCtx: bgCtx,
	}
	dispatcher := &asyncDispatcher{ctx: bgCtx, wg: &a.bgWG}
	// Wire traffic.Service into the panel-wide WaitGroup. Its async
	// floor-push + quota-event email goroutines (`safego.GoTracked`)
	// now register with bgWG so App.Shutdown drains them before exit.
	trafficSvc.SetBgWG(&a.bgWG)

	// --- transport layer ---
	httpHandler := httptransport.NewRouter(httptransport.Deps{
		Async:            dispatcher,
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
		Mail:             mailSvc,
		Reconcile:        reconcileSvc,
		SubPerIPPerMin:   sysSettings.SubPerIPPerMin,
		LoginPerIPPerMin: sysSettings.LoginPerIPPerMin,
	})

	// Health check ticks more often than reconcile because a "node is
	// reachable" answer goes stale fast — admins want a green dot that
	// matches the last few minutes, not the last 15. Default 5 min; tied
	// to the existing traffic-pull cadence since they're both "talk to
	// 3X-UI" passes and admins set one number for the whole panel rhythm.
	healthInterval := time.Duration(sysSettings.CronTrafficPullMinutes) * time.Minute
	if healthInterval <= 0 {
		healthInterval = 5 * time.Minute
	}

	a.traffic = trafficSvc
	a.reconcile = reconcileSvc
	a.user = userSvc
	a.node = nodeSvc
	a.audit = auditSvc
	a.mail = mailSvc
	a.health = healthSvc
	a.settings = repos.Settings
	a.syncTasks = repos.SyncTask
	a.trafficRepo = repos.Traffic
	a.nodeTraffic = repos.NodeTraffic
	a.rollup = rollup.New(db)
	a.repos = repos
	a.saml = samlSvc
	a.xuiPool = pool
	a.trafficInterval = time.Duration(sysSettings.CronTrafficPullMinutes) * time.Minute
	a.reconcileInterval = time.Duration(sysSettings.CronReconcileMinutes) * time.Minute
	a.healthInterval = healthInterval
	a.server = &http.Server{
		Addr:    cfg.Listen,
		Handler: httpHandler,
		// ReadHeaderTimeout caps slow-header attacks (Slowloris on the
		// header phase); ReadTimeout caps slow-body attacks; WriteTimeout
		// caps slow-consumer attacks on the response side. IdleTimeout
		// closes leaked keep-alive sockets.
		//
		// WriteTimeout is intentionally generous (5 min) because rendered
		// subscriptions for users with many nodes + a regenerated rule_set
		// can serialise into a 500 KB+ YAML/Clash blob on a slow uplink.
		// Sub render itself is bounded by 3X-UI HTTP timeouts + the
		// render pipeline; 5 min is a safety net, not a steady-state.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
	return a, nil
}

// Run binds the listen socket, then bootstraps the initial admin (if
// missing), starts background workers, and finally serves HTTP until
// the listener stops.
//
// Why bind FIRST: initAdminIfNeeded prints the bootstrap admin password
// via log.Info. If the panel exits before the user can read that line
// (e.g., port already in use, the terminal window closes), the password
// is lost — and the next start sees the row already there, so the
// password is never re-printed. By binding before bootstrap we fail
// fast on the boring errors that would otherwise eat the secret.
func (a *App) Run() error {
	ln, err := net.Listen("tcp", a.server.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", a.server.Addr, err)
	}

	if err := initAdminIfNeeded(context.Background(), a.repos); err != nil {
		_ = ln.Close()
		return fmt.Errorf("init admin: %w", err)
	}

	bgCtx := a.bgRootCtx

	// Every background worker runs under safego.GoTracked: the *recover*
	// shield keeps a single nil-deref / map race in a 3X-UI response from
	// tearing the panel down, and the WaitGroup lets Shutdown drain them
	// before the process exits.
	a.saml.StartMetadataRefresh(bgCtx, &a.bgWG)
	// Boot-time 3X-UI compat probe: fire once on startup so an unexpected
	// version surfaces in boot logs immediately rather than waiting for
	// the first traffic-poll cycle. Subsequent re-probes piggyback the
	// traffic poll loop (see runTrafficLoop) — no independent ticker, the
	// cadence naturally matches "PSP is talking to every panel anyway".
	safego.GoTracked(&a.bgWG, "boot-version-probe", func() { a.probePanelVersionsOnce(bgCtx) })
	safego.GoTracked(&a.bgWG, "sync-task-loop", func() { a.runSyncTaskLoop(bgCtx) })
	safego.GoTracked(&a.bgWG, "audit-cleanup-loop", func() { a.runAuditCleanupLoop(bgCtx) })
	safego.GoTracked(&a.bgWG, "traffic-loop", func() { a.runTrafficLoop(bgCtx) })
	safego.GoTracked(&a.bgWG, "mail-loop", func() { a.runMailLoop(bgCtx) })
	safego.GoTracked(&a.bgWG, "reconcile-loop", func() { a.runReconcileLoop(bgCtx) })
	safego.GoTracked(&a.bgWG, "health-loop", func() { a.runHealthLoop(bgCtx) })

	return a.server.Serve(ln)
}

func (a *App) runHealthLoop(ctx context.Context) {
	if a.health == nil {
		return
	}
	log.Info("node health check loop started", "interval", a.healthInterval.String())
	a.health.Loop(ctx, a.healthInterval)
}

// probePanelVersionsOnce hits /panel/api/server/status on every configured
// panel sequentially, records the version snapshot via UpdateVersion, and
// logs Warn for any compat mismatch. Sequential rather than parallel — there
// are typically only a handful of panels; parallelism would complicate
// per-panel timeout handling without a meaningful win. Failures are tolerated:
// an unreachable panel just gets its versions cleared and a Warn line, the
// remaining panels continue.
func (a *App) probePanelVersionsOnce(ctx context.Context) {
	if a.xuiPool == nil || a.repos.XUIPanel == nil {
		return
	}
	panels, err := a.repos.XUIPanel.List(ctx)
	if err != nil {
		log.Warn("compat probe: list panels", "err", err)
		return
	}
	if len(panels) == 0 {
		return
	}
	log.Debug("compat probe tick", "panel_count", len(panels))
	// Pull both GitHub-backed snapshots BEFORE iterating panels:
	//   - RefreshRemoteCompat: per-major compat JSON → drives the tested
	//     range used by CheckXUI. Without this, every panel's compat
	//     status renders as Unknown until admin clicks Test (and the
	//     "compat data not loaded" tooltip appears in the version cell).
	//   - RefreshLatestXUI: the latest 3X-UI release tag → drives the
	//     ⋮ kebab "update available" badge.
	// Both are single-flight + throttled internally, so the boot tick
	// plus a concurrent Servers-page open collapse to one call each.
	// 10 s budget per call: GitHub raw + API are both usually sub-second.
	compatCtx, compatCancel := context.WithTimeout(ctx, 10*time.Second)
	if rerr := version.RefreshRemoteCompat(compatCtx, ""); rerr != nil {
		log.Debug("compat probe: refresh remote compat failed (offline / rate limited?)", "err", rerr)
	}
	compatCancel()
	latestCtx, latestCancel := context.WithTimeout(ctx, 10*time.Second)
	if rerr := version.RefreshLatestXUI(latestCtx); rerr != nil {
		log.Debug("compat probe: refresh latest 3X-UI failed (offline / rate limited?)", "err", rerr)
	}
	latestCancel()
	for _, p := range panels {
		if ctx.Err() != nil {
			return
		}
		c, err := a.xuiPool.Get(p.ID)
		if err != nil {
			log.Warn("compat probe: pool get", "panel_id", p.ID, "panel_name", p.Name, "err", err)
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		status, err := c.GetServerStatus(probeCtx)
		cancel()
		now := time.Now()
		if err != nil {
			log.Warn("compat probe failed", "panel_id", p.ID, "panel_name", p.Name, "err", err)
			// Record only the timestamp — preserves the last-known-good
			// panel_version / xray_version so a transient probe failure
			// (network blip, panel restart in progress) doesn't wipe
			// the UI's stale-but-useful snapshot. UI consults
			// version_checked_at to display freshness.
			if uerr := a.repos.XUIPanel.UpdateVersionCheckedAt(ctx, p.ID, now); uerr != nil {
				log.Warn("compat probe: write checked-at", "panel_id", p.ID, "err", uerr)
			}
			continue
		}
		compatStatus := version.CheckXUI(status.PanelVersion)
		switch compatStatus {
		case version.CompatSupported:
			// Supported is the steady state on every 10-min tick; keep it
			// at Debug so a healthy fleet doesn't fill logs. Admin who
			// wants to see "yes, the probe ran ok" can flip PSP_LOG_LEVEL.
			log.Debug("panel version ok",
				"panel_id", p.ID, "panel_name", p.Name,
				"panel_version", status.PanelVersion,
				"xray_version", status.XrayVersion,
				"xray_state", status.XrayState)
		default:
			log.Warn("panel compat warning",
				"panel_id", p.ID, "panel_name", p.Name,
				"panel_version", status.PanelVersion,
				"xray_version", status.XrayVersion,
				"xray_state", status.XrayState,
				"compat", compatStatus.String(),
				"detail", version.CompatMessage(status.PanelVersion, compatStatus))
		}
		if uerr := a.repos.XUIPanel.UpdateVersion(ctx, p.ID, status.PanelVersion, status.XrayVersion, &now); uerr != nil {
			log.Warn("compat probe: write version", "panel_id", p.ID, "err", uerr)
		}
	}
}

func (a *App) runMailLoop(ctx context.Context) {
	interval := time.Hour
	t := time.NewTicker(interval)
	defer t.Stop()
	log.Info("mail reminder loop started", "interval", interval.String())
	for {
		if a.mail != nil {
			if err := a.mail.ProcessReminders(ctx); err != nil {
				log.Warn("mail reminders", "err", err)
			}
			// Process due mail notification tasks (retries).
			if err := a.mail.ProcessDueMailTasks(ctx, 20); err != nil {
				log.Warn("mail tasks", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *App) runAuditCleanupLoop(ctx context.Context) {
	interval := time.Hour
	t := time.NewTicker(interval)
	defer t.Stop()
	log.Info("audit cleanup loop started", "interval", interval.String())
	for {
		// Rollup before prune so the freshly-completed hour is captured in
		// hourly BEFORE raw retention has a chance to delete it. Without
		// this ordering the first tick after a panel boot could lose data
		// older than `rawTrafficRetentionDays` that hadn't yet been
		// downsampled.
		a.runRollup(ctx)
		a.pruneAudit(ctx)
		a.pruneSyncTasks(ctx)
		a.pruneTrafficSnapshots(ctx)
		a.pruneMailSent(ctx)
		a.pruneSubLogs(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *App) runRollup(ctx context.Context) {
	if a.rollup == nil {
		return
	}
	if err := a.rollup.RollupOnce(ctx); err != nil {
		log.Warn("traffic rollup", "err", err)
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

// rawTrafficRetentionDays is the fixed retention window on the raw 5-min
// snapshot tables (traffic_snapshots / client_traffic_snapshots /
// node_traffic_snapshots). It is NOT admin-tunable — its only job is to
// cover the current incomplete day plus a small catch-up buffer for the
// rollup cron. Long-window history lives on the *_hourly tables and is
// gated by TrafficHistoryDays.
const rawTrafficRetentionDays = 7

// pruneTrafficSnapshots clears traffic snapshot rows the panel no longer
// needs:
//   - raw tables (5-min): pruned at the hardcoded `rawTrafficRetentionDays`
//     window; covers today + a few days of headroom for chart "today" math
//   - *_hourly tables: pruned at the admin-tunable `TrafficHistoryDays`
//     setting, which is also the user-visible chart history depth
//
// Mirrors pruneAudit / pruneSyncTasks: load settings, compute cutoffs,
// call repo prune helpers, log non-zero deletions.
// hourAlignedCutoff returns the start of the UTC hour containing
// (now - retentionDays). Raw snapshot pruning uses it so deletes land on
// whole-hour boundaries (see pruneTrafficSnapshots for why).
func hourAlignedCutoff(now time.Time, retentionDays int) time.Time {
	c := now.AddDate(0, 0, -retentionDays).UTC()
	return time.Date(c.Year(), c.Month(), c.Day(), c.Hour(), 0, 0, 0, time.UTC)
}

func (a *App) pruneTrafficSnapshots(ctx context.Context) {
	if a.settings == nil || a.trafficRepo == nil {
		return
	}
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		log.Warn("traffic snapshot cleanup load settings", "err", err)
		return
	}

	// Hour-align the cutoff so a prune only ever deletes WHOLE UTC hours of raw
	// snapshots. Rollup buckets by hourFloor(captured_at) and re-scans every
	// surviving raw row each cycle with an unconditional upsert; if a prune
	// removed only the earliest rows of an hour that still has later rows, the
	// next rollup would recompute a smaller MAX-MIN delta and overwrite the
	// already-correct hourly bucket (silent history regression). Whole-hour
	// deletes keep every surviving hour complete, so its delta stays stable.
	rawCutoff := hourAlignedCutoff(time.Now(), rawTrafficRetentionDays)
	deleted, err := a.trafficRepo.PruneBefore(ctx, rawCutoff)
	if err != nil {
		log.Warn("traffic snapshot cleanup", "err", err)
	} else if deleted > 0 {
		log.Info("traffic snapshot cleanup", "deleted", deleted, "retention_days", rawTrafficRetentionDays)
	}
	if a.nodeTraffic != nil {
		nodeDeleted, err := a.nodeTraffic.PruneBefore(ctx, rawCutoff)
		if err != nil {
			log.Warn("node traffic snapshot cleanup", "err", err)
		} else if nodeDeleted > 0 {
			log.Info("node traffic snapshot cleanup", "deleted", nodeDeleted, "retention_days", rawTrafficRetentionDays)
		}
	}

	if settings.TrafficHistoryDays > 0 {
		hourlyCutoff := time.Now().AddDate(0, 0, -settings.TrafficHistoryDays)
		hourlyDeleted, err := a.trafficRepo.PruneHourlyBefore(ctx, hourlyCutoff)
		if err != nil {
			log.Warn("traffic hourly cleanup", "err", err)
		} else if hourlyDeleted > 0 {
			log.Info("traffic hourly cleanup", "deleted", hourlyDeleted, "retention_days", settings.TrafficHistoryDays)
		}
		if a.nodeTraffic != nil {
			nodeHourlyDeleted, err := a.nodeTraffic.PruneHourlyBefore(ctx, hourlyCutoff)
			if err != nil {
				log.Warn("node traffic hourly cleanup", "err", err)
			} else if nodeHourlyDeleted > 0 {
				log.Info("node traffic hourly cleanup", "deleted", nodeHourlyDeleted, "retention_days", settings.TrafficHistoryDays)
			}
		}
	}
}

// pruneMailSent deletes mail_sent rows older than MailSentRetentionDays.
// The table doubles as both a "already sent, don't resend" dedup key and
// an admin-facing email audit log (Logs → Email tab), so retention is
// admin-tunable in the notify settings rather than hardcoded.
func (a *App) pruneMailSent(ctx context.Context) {
	if a.settings == nil || a.repos.Mail == nil {
		return
	}
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		log.Warn("mail sent cleanup load settings", "err", err)
		return
	}
	if settings.MailSentRetentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -settings.MailSentRetentionDays)
	deleted, err := a.repos.Mail.DeleteSentBefore(ctx, cutoff)
	if err != nil {
		log.Warn("mail sent cleanup", "err", err)
		return
	}
	if deleted > 0 {
		log.Info("mail sent cleanup", "deleted", deleted, "retention_days", settings.MailSentRetentionDays)
	}
}

// pruneSubLogs deletes sub_logs rows older than SubLogRetentionDays.
// The admin UI has its own manual purge button, but a long-running
// instance without periodic prune accumulates one row per subscription
// fetch (every client refresh hits this) — unbounded growth otherwise.
func (a *App) pruneSubLogs(ctx context.Context) {
	if a.settings == nil || a.repos.SubLog == nil {
		return
	}
	settings, err := a.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		log.Warn("sub log cleanup load settings", "err", err)
		return
	}
	if settings.SubLogRetentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -settings.SubLogRetentionDays)
	deleted, err := a.repos.SubLog.DeleteBefore(ctx, cutoff)
	if err != nil {
		log.Warn("sub log cleanup", "err", err)
		return
	}
	if deleted > 0 {
		log.Info("sub log cleanup", "deleted", deleted, "retention_days", settings.SubLogRetentionDays)
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

// Shutdown stops background workers and gracefully closes the HTTP
// server. Cancellation order:
//  1. cancel bgRootCtx — every loop sees ctx.Done() and exits its select
//  2. server.Shutdown(ctx) — stop accepting new requests, drain in-flight
//  3. wait for bgWG up to the caller-supplied deadline — guarantees a
//     stuck SMTP / 3X-UI HTTP call doesn't leave a half-committed
//     transaction or leaked connection behind
//
// The caller controls the overall deadline through ctx; if the workers
// don't return in time we log and continue rather than block forever.
func (a *App) Shutdown(ctx context.Context) error {
	if a.bgCancel != nil {
		a.bgCancel()
	}
	httpErr := a.server.Shutdown(ctx)

	done := make(chan struct{})
	go func() {
		a.bgWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		log.Warn("shutdown: background workers did not exit before deadline")
	}
	return httpErr
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
			// Piggyback the 3X-UI compat re-probe onto the traffic poll
			// cadence — PSP is already in a "talk to every panel" cycle,
			// adding one /server/status call per panel is cheap (~10ms
			// each) and avoids an independent ticker.
			//
			// Run it in its OWN goroutine: a single unreachable panel
			// adds 10s of `GetServerStatus` wait per probe call, and
			// with N down panels the inline-version-probe used to push
			// the next traffic tick past its interval (the ticker just
			// keeps firing, but the loop is single-threaded so it misses
			// cycles entirely). Decoupling means probe slowness can't
			// starve traffic. Tracked via bgWG so Shutdown still drains.
			safego.GoTracked(&a.bgWG, "compat-reprobe", func() {
				if !a.compatProbeInflight.CompareAndSwap(false, true) {
					log.Debug("compat probe skipped (previous still running)")
					return
				}
				defer a.compatProbeInflight.Store(false)
				a.probePanelVersionsOnce(ctx)
			})
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
		UPN:                "admin",
		PasswordHash:       string(hash),
		Role:               domain.RoleAdmin,
		SubToken:           subToken,
		UUID:               idgen.NewUUID(),
		GroupID:            g.ID,
		TrafficResetPeriod: domain.ResetMonthly,
		TrafficPeriodStart: &now,
		Enabled:            true,
		Remark:             "bootstrap admin",
		SSOProvider:        domain.SSOProviderLocal,
		SSOSubject:         "admin",
	}
	if err := repos.User.Create(ctx, u); err != nil {
		return err
	}

	log.Info("bootstrap admin created",
		"upn", u.UPN,
		"password", pw,
		"user_id", u.ID,
		"group", g.Slug)
	return nil
}
