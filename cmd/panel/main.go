// Package main is the panel binary entrypoint. The build is intentionally
// minimal: load config, hand off to app.Build, install signal handler.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
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
	"github.com/KazuhaHub/passwall-sub-panel/internal/migrate"
	"github.com/KazuhaHub/passwall-sub-panel/internal/seed"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// applyLogLevelFromEnv honors PSP_LOG_LEVEL (debug / info / warn / error,
// case-insensitive) and adjusts the global slog level before the panel does
// any logging worth filtering. Unrecognized / empty values leave the default
// Info baseline alone. Mostly used to unlock the per-stage timing in
// PollOnce on demand (see traffic.Service.PollOnce / beta.14 changelog).
func applyLogLevelFromEnv() {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PSP_LOG_LEVEL"))) {
	case "debug":
		pkglog.SetLevel(stdlog.LevelDebug)
	case "info":
		pkglog.SetLevel(stdlog.LevelInfo)
	case "warn", "warning":
		pkglog.SetLevel(stdlog.LevelWarn)
	case "error":
		pkglog.SetLevel(stdlog.LevelError)
	}
}

func ensureDirs(cfg *config.Config) {
	for _, d := range []string{cfg.ConfigDir, cfg.DataDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatalf("create directory %s: %v", d, err)
		}
	}
}

const defaultConfigPath = "config.yaml"

func main() {
	// Subcommand dispatch. Currently only `migrate` is intercepted so a
	// `psp` invocation with no args (or with --config) still falls through
	// to the normal panel boot. Keeping this BEFORE config load / flag
	// parsing means `migrate`'s own FlagSet owns its argv and doesn't
	// collide with the panel's --config flag.
	//
	// Upgrade policy (see docs/ARCHITECTURE.md §16): the embedded migrator
	// only handles the immediately previous major version → current. Older
	// installs upgrade through each major in turn (vN-2 → vN-1 → vN).
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		os.Exit(migrate.Run(os.Args[2:]))
	}
	// `psp version` prints the version then exits — useful in scripts /
	// CI to confirm the deployed binary matches the release tag.
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		log.Printf("%s", version.String())
		if version.BuildDate != "" {
			log.Printf("built %s", version.BuildDate)
		}
		return
	}

	// Main panel flags. Subcommands above (migrate / version) own their own
	// argv so flag.Parse() here only sees the panel boot path. --debug is a
	// shortcut equivalent to setting PSP_LOG_LEVEL=debug; both are honored
	// (env first, flag last — flag wins on conflict).
	cfgPathFlag := flag.String("config", "", "main config path")
	debugFlag := flag.Bool("debug", false, "enable debug-level logging (equivalent to PSP_LOG_LEVEL=debug); unlocks per-stage timing in PollOnce and similar diagnostic logs")
	flag.Parse()

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	applyLogLevelFromEnv()
	if *debugFlag {
		pkglog.SetLevel(stdlog.LevelDebug)
	}

	cfgPath := resolveConfigPath(*cfgPathFlag)
	cfg, err := config.LoadOrGenerate(cfgPath)
	if err != nil {
		log.Fatalf("load config %s: %v", cfgPath, err)
	}
	ensureDirs(cfg)

	// Release baked-in default rulesets / templates into ConfigDir when
	// they're missing. Lets a fresh systemd / Docker bind-mount deploy run
	// without manual file copying. Idempotent: existing files are preserved.
	if err := seed.Ensure(cfg.ConfigDir); err != nil {
		log.Fatalf("seed defaults into %s: %v", cfg.ConfigDir, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, err := app.Build(ctx, cfg)
	if err != nil {
		log.Fatalf("build app: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("Passwall-Sub-Panel %s listening on %s", version.String(), cfg.Listen)
		if err := a.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-quit:
		log.Printf("got signal %s, shutting down...", sig)
	case err := <-errCh:
		log.Printf("server error: %v, shutting down...", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := a.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// resolveConfigPath picks the panel's config path with --config > PSP_CONFIG >
// the bundled default. flag parsing happens in main() so this stays argv-free.
func resolveConfigPath(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("PSP_CONFIG"); v != "" {
		return v
	}
	return defaultConfigPath
}
