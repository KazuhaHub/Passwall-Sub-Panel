// Package main is the panel binary entrypoint. The build is intentionally
// minimal: load config, hand off to app.Build, install signal handler.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	stdlog "log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/app"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	pkglog "github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/seed"
	"github.com/KazuhaHub/passwall-sub-panel/internal/servermigrate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/upnnorm"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// parseLogLevel returns (level, true) for known names (case-insensitive);
// returns (LevelInfo, false) for empty / unknown. Used by both the early-boot
// env/flag pass and the post-config-load fallback so the priority chain stays
// consistent.
func parseLogLevel(s string) (stdlog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return stdlog.LevelDebug, true
	case "info":
		return stdlog.LevelInfo, true
	case "warn", "warning":
		return stdlog.LevelWarn, true
	case "error":
		return stdlog.LevelError, true
	}
	return stdlog.LevelInfo, false
}

// applyEarlyLogLevel applies --debug or PSP_LOG_LEVEL NOW (before config.Load
// runs) so that boot-time logs — including the config load itself, if it
// errors — are filtered at the verbosity the operator asked for. Returns true
// if either source set the level; main then knows whether the config.LogLevel
// field (loaded later) should kick in. Priority within this function: flag
// wins over env.
func applyEarlyLogLevel(debugFlag bool) bool {
	if debugFlag {
		pkglog.SetLevel(stdlog.LevelDebug)
		return true
	}
	if env := os.Getenv("PSP_LOG_LEVEL"); env != "" {
		if lvl, ok := parseLogLevel(env); ok {
			pkglog.SetLevel(lvl)
			return true
		}
	}
	return false
}

func ensureDirs(cfg *config.Config) error {
	for _, d := range []string{cfg.ConfigDir, cfg.DataDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", d, err)
		}
	}
	return nil
}

func fatalLog(msg string, args ...any) {
	pkglog.Error(msg, args...)
	os.Exit(1)
}

const retiredMigrateMessage = "ERROR: psp migrate (V2→V3) was retired in V4. Use the frozen PSP v3.9.2 release to migrate V2→V3, then finish the V3 upgrade. The final V3 release (v3.9.2 / v3.9.2-beta.20) upgrades to V4 automatically on normal startup; no migrate command is needed."

func main() {
	// V4 no longer embeds the V2→V3 offline migrator. Keep its command name
	// reserved BEFORE config load and flag parsing: flag.Parse ignores trailing
	// positional arguments, so simply deleting the dispatch could accidentally
	// start the panel (and migrate its configured database) instead of refusing.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		pkglog.Error(retiredMigrateMessage)
		os.Exit(2)
	}
	// `psp normalize-upn` folds stored login names to their canonical form.
	// Operator-invoked rather than a boot migration: folding can violate the
	// users.upn unique index on an install that already holds a near-duplicate
	// pair, and neither aborting at boot nor silently rewriting an admin's
	// login name is acceptable while the panel is coming up. Dry run unless
	// --apply. Owns its own FlagSet, separate from panel boot flags.
	if len(os.Args) > 1 && os.Args[1] == "normalize-upn" {
		os.Exit(upnnorm.Run(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate-server" {
		os.Exit(servermigrate.Run(os.Args[2:]))
	}
	// `psp version` prints the version then exits — useful in scripts /
	// CI to confirm the deployed binary matches the release tag.
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		pkglog.Info("version", "value", version.String())
		if version.BuildDate != "" {
			pkglog.Info("build", "date", version.BuildDate)
		}
		return
	}

	// Main panel flags. Subcommands above are handled before flag.Parse(),
	// which only sees the panel boot path. --debug is a
	// shortcut equivalent to setting PSP_LOG_LEVEL=debug; both are honored
	// (env first, flag last — flag wins on conflict).
	cfgPathFlag := flag.String("config", "", "main config path")
	debugFlag := flag.Bool("debug", false, "enable debug-level logging (equivalent to PSP_LOG_LEVEL=debug); unlocks per-stage timing in PollOnce and similar diagnostic logs")
	flag.Parse()
	// Also refuse unconsumed commands after boot flags, e.g. --config x migrate.
	// Go's flag package stops at the first positional argument without an error;
	// ignoring it would turn an attempted maintenance command into daemon boot.
	if flag.NArg() != 0 {
		if flag.Arg(0) == "migrate" {
			pkglog.Error(retiredMigrateMessage)
		} else {
			pkglog.Error("unexpected positional arguments",
				"detail", "Panel startup accepts only --config and --debug; commands (version / normalize-upn / migrate-server) must come first.")
		}
		os.Exit(2)
	}

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	// Log level priority: --debug flag > PSP_LOG_LEVEL env > config.log_level
	// > default (info). Flag/env apply NOW so any config-load error logs at
	// the right verbosity; the config-file fallback kicks in only if neither
	// flag nor env set anything explicit.
	earlyLevelSet := applyEarlyLogLevel(*debugFlag)

	cfgPath := config.ResolvePath(*cfgPathFlag)
	cfg, err := config.LoadOrGenerate(cfgPath)
	if err != nil {
		fatalLog("load config", "path", cfgPath, "err", err)
	}
	if !earlyLevelSet && cfg.LogLevel != "" {
		if lvl, ok := parseLogLevel(cfg.LogLevel); ok {
			pkglog.SetLevel(lvl)
		}
	}
	if err := ensureDirs(cfg); err != nil {
		fatalLog("prepare directories", "err", err)
	}

	// Release baked-in default rulesets / templates into ConfigDir when
	// they're missing. Lets a fresh systemd / Docker bind-mount deploy run
	// without manual file copying. Idempotent: existing files are preserved.
	if err := seed.Ensure(cfg.ConfigDir); err != nil {
		fatalLog("seed defaults", "config_dir", cfg.ConfigDir, "err", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, err := app.Build(ctx, cfg)
	if err != nil {
		fatalLog("build app", "err", err)
	}

	errCh := make(chan error, 1)
	go func() {
		pkglog.Info("server listening", "version", version.String(), "address", cfg.Listen)
		if err := a.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-quit:
		pkglog.Info("shutdown requested", "signal", sig.String())
	case err := <-errCh:
		pkglog.Error("server error; shutting down", "err", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := a.Shutdown(shutdownCtx); err != nil {
		pkglog.Error("shutdown failed", "err", err)
	}
}

// resolveConfigPath picks the panel's config path with --config > PSP_CONFIG >
// the bundled default. flag parsing happens in main() so this stays argv-free.
