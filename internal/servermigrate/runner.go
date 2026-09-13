// Package servermigrate is the stopped-panel composition root for
// `psp migrate-server`. It never builds the application, maintains the schema,
// launches workers, calls the old server, or prints installation credentials.
package servermigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/servermigration"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type previewer interface {
	Preview(context.Context, int64, string, bool) (*domain.ServerMigrationPreview, error)
}

type connection struct {
	preview previewer
	repo    ports.ServerMigrationRepo
	close   func()
}

type opener func(context.Context, string) (*connection, error)

// Run returns 0 on success, 1 on a blocked/failed operation, and 2 for usage.
func Run(args []string) int {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return run(ctx, args, os.Stdout, os.Stderr, openConfiguredDatabase)
}

func run(ctx context.Context, args []string, out, errOut io.Writer, open opener) int {
	fs := flag.NewFlagSet("migrate-server", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() {
		fmt.Fprintln(errOut, "Usage: psp migrate-server --server-id ID [--config PATH] [--core-version VERSION]")
		fmt.Fprintln(errOut, "Dry run by default. Preserves the existing 3X-UI server, node and client IDs,")
		fmt.Fprintln(errOut, "PSP-managed config, credentials, memberships and historical traffic.")
		fmt.Fprintln(errOut, "Before --apply: back up the database AND configuration; fully stop ALL PSP")
		fmt.Fprintln(errOut, "processes using this database and stop old Xray on the node (an offline panel")
		fmt.Fprintln(errOut, "does not prove Xray stopped). Confirm these conditions explicitly below.")
		fmt.Fprintln(errOut, "Only PSP-managed snapshots migrate. Global routing/DNS/outbounds, manual")
		fmt.Fprintln(errOut, "clients/inbounds and external files do not. Verify no required dependency is lost.")
		fmt.Fprintln(errOut, "A configuration change since preview is rejected; rerun preview instead of guessing.")
		fmt.Fprintln(errOut, "Restart PSP after conversion, then use Install Passwall Node on the SAME server.")
		fs.PrintDefaults()
	}
	id := fs.Int64("server-id", 0, "existing 3X-UI server database ID")
	cfgPath := fs.String("config", "", "existing config path; honors PSP_CONFIG and normal env overrides")
	coreVersion := fs.String("core-version", "", "explicit verified Xray version (default: preserve known current core)")
	allowRestricted := fs.Bool("allow-restricted-reality", false, "acknowledge the selected verified core's restricted REALITY compatibility")
	fingerprint := fs.String("expected-fingerprint", "", "64-character fingerprint from the current migration preview")
	allStopped := fs.Bool("all-psp-stopped", false, "confirm ALL PSP processes sharing this database have completely exited")
	oldStopped := fs.Bool("old-xray-stopped", false, "confirm old Xray on this server is stopped and cannot automatically restart")
	managedOnly := fs.Bool("managed-only", false, "confirm PSP-managed nodes do not require unmigrated global settings, manual objects or files")
	apply := fs.Bool("apply", false, "atomically convert the existing record (default: dry run)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || *id <= 0 {
		fmt.Fprintln(errOut, "ERROR: a positive --server-id is required; positional arguments are not accepted.")
		return 2
	}
	if *apply {
		digest, err := hex.DecodeString(*fingerprint)
		if !*allStopped || !*oldStopped || !*managedOnly || *coreVersion == "" || err != nil || len(digest) != sha256.Size || *fingerprint != strings.ToLower(*fingerprint) {
			fmt.Fprintln(errOut, "ERROR: --apply requires --all-psp-stopped, --old-xray-stopped, --managed-only, an explicit --core-version and a valid --expected-fingerprint. Back up first.")
			return 2
		}
	}
	conn, err := open(ctx, config.ResolvePath(*cfgPath))
	if err != nil {
		// Do not echo driver/config errors: DSNs and SQL can contain secrets.
		fmt.Fprintln(errOut, "ERROR: cannot open the existing current V4 database/configuration; no conversion performed. Check the configured database, key material and completed V4 startup.")
		return 1
	}
	if conn.close != nil {
		defer conn.close()
	}
	preview, err := conn.preview.Preview(ctx, *id, *coreVersion, *allowRestricted)
	if err != nil || preview == nil {
		fmt.Fprintln(errOut, "ERROR: cannot preview this existing 3X-UI server; no conversion performed.")
		return 1
	}
	fmt.Fprintf(out, "server: %d %q\nXray: %s\nPSP nodes: %d; clients: %d\nfingerprint: %s\n", preview.ServerID, preview.ServerName, preview.CoreVersion, preview.NodeCount, preview.ClientCount, preview.Fingerprint)
	for _, issue := range preview.Blockers {
		fmt.Fprintf(out, "BLOCKED: %s (node=%d client=%d)\n", issue.Code, issue.NodeID, issue.ClientID)
	}
	for _, issue := range preview.Warnings {
		fmt.Fprintf(out, "WARNING: %s (node=%d client=%d)\n", issue.Code, issue.NodeID, issue.ClientID)
	}
	if !preview.CanMigrate || len(preview.Blockers) > 0 {
		fmt.Fprintln(errOut, "ERROR: resolve preflight blockers before conversion; nothing was changed.")
		return 1
	}
	if !*apply {
		fmt.Fprintln(out, "Dry run — no rows or credentials were changed. Back up, stop all PSP instances and old Xray, then apply with this fingerprint and explicit core version.")
		return 0
	}
	if preview.Fingerprint != *fingerprint {
		fmt.Fprintln(errOut, "ERROR: configuration changed since preview; no conversion performed. Review a fresh preview.")
		return 1
	}
	identity, err := idgen.NewSubToken()
	if err != nil {
		fmt.Fprintln(errOut, "ERROR: cannot generate node identity; no conversion performed.")
		return 1
	}
	secret, err := idgen.NewSubToken()
	if err != nil {
		fmt.Fprintln(errOut, "ERROR: cannot generate node credential; no conversion performed.")
		return 1
	}
	raw := "pspn_" + secret
	sum := sha256.Sum256([]byte(raw))
	agent := &domain.NodeAgent{
		AgentID: "agt_" + identity, PanelID: *id, CredentialSHA256: hex.EncodeToString(sum[:]),
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: preview.CoreVersion,
		AllowRestrictedReality: preview.AllowRestrictedReality,
	}
	if err := conn.repo.Apply(ctx, *id, *fingerprint, agent, raw); err != nil {
		// A connection failure while acknowledging COMMIT can be ambiguous:
		// inspect the original row instead of claiming the transaction rolled back.
		fmt.Fprintln(errOut, "ERROR: conversion result could not be confirmed; inspect the original server record/current database state before retrying or restarting old Xray. Do not delete or recreate the server.")
		return 1
	}
	fmt.Fprintf(out, "Converted server %d in place. Restart PSP, then open this SAME server's Install Passwall Node action. Its fixed installation credential is stored encrypted, not printed here.\n", *id)
	fmt.Fprintln(out, "Historical traffic is retained; bytes not polled from old Xray before it stopped cannot be recovered. Wait for the real node/core to report applied state before treating migration as complete.")
	return 0
}

func openConfiguredDatabase(ctx context.Context, path string) (*connection, error) {
	cfg, err := config.Load(path) // never generate a new config during maintenance
	if err != nil {
		return nil, err
	}
	if cfg.DBKind() == "sqlite" {
		info, err := os.Stat(cfg.DBDSN())
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("maintenance requires an existing SQLite file")
		}
	}
	db, err := sqlstore.OpenQuiet(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		return nil, err
	}
	db = db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if err := sqlstore.RequireCurrentV4Schema(db); err != nil {
		sqlDB.Close()
		return nil, err
	}
	sqlstore.ConfigureSecretKey(cfg.SecretKeyMaterial())
	repo := sqlstore.NewRepos(db).ServerMigration
	return &connection{preview: servermigration.New(repo), repo: repo, close: func() { sqlDB.Close() }}, nil
}
