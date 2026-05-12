// Package main is the operational CLI: init-admin to seed the first
// admin row, encrypt to prepare AES-GCM ciphertext for xui_panels.yaml.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/mysql"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/crypto"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/idgen"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "init-admin":
		initAdmin(os.Args[2:])
	case "encrypt":
		encryptValue(os.Args[2:])
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage:
  psp-cli init-admin --username <name> [--password <pw>] [--config <path>]
      Create the first local admin in the panel DB. Auto-generates a
      password if one is not supplied. Also seeds a "default" group if
      none exists yet.

  psp-cli encrypt --value <plaintext>
      AES-GCM-encrypt <plaintext> with PSP_SECRET_KEY (env). Prints the
      result with an "enc:" prefix, ready to paste into xui_panels.yaml.`)
}

func initAdmin(args []string) {
	fs := flag.NewFlagSet("init-admin", flag.ExitOnError)
	cfgPath := fs.String("config", "config/config.yaml", "main config path")
	username := fs.String("username", "", "admin username (required)")
	password := fs.String("password", "", "initial password (auto-generated if empty)")
	_ = fs.Parse(args)

	if *username == "" {
		fatal("--username is required")
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal("load config: %v", err)
	}
	db, err := mysql.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		fatal("db open: %v", err)
	}
	if err := mysql.Migrate(db); err != nil {
		fatal("db migrate: %v", err)
	}
	repos := mysql.NewRepos(db)
	ctx := context.Background()

	// Make sure there's a group to assign the admin to.
	g, err := repos.Group.GetBySlug(ctx, "default")
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			fatal("lookup default group: %v", err)
		}
		g = &domain.Group{
			Slug:      "default",
			Name:      "Default",
			TagFilter: domain.TagFilter{All: true},
		}
		if err := repos.Group.Create(ctx, g); err != nil {
			fatal("create default group: %v", err)
		}
	}

	// Refuse to overwrite an existing user.
	if existing, err := repos.User.GetByUsername(ctx, *username); err == nil && existing != nil {
		fatal("user %q already exists", *username)
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		fatal("lookup user: %v", err)
	}

	pw := *password
	if pw == "" {
		pw, err = idgen.NewPassword()
		if err != nil {
			fatal("generate password: %v", err)
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		fatal("bcrypt: %v", err)
	}
	subToken, err := idgen.NewSubToken()
	if err != nil {
		fatal("generate sub token: %v", err)
	}
	now := time.Now()
	u := &domain.User{
		Username:           *username,
		Source:             domain.UserSourceLocal,
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
		fatal("create user: %v", err)
	}

	fmt.Printf("Admin created.\n")
	fmt.Printf("  username: %s\n", u.Username)
	fmt.Printf("  password: %s\n", pw)
	fmt.Printf("  user_id:  %d\n", u.ID)
	fmt.Printf("  group:    %s (id=%d)\n", g.Slug, g.ID)
	fmt.Printf("\nKeep the password — it is not stored in plaintext anywhere.\n")
}

func encryptValue(args []string) {
	fs := flag.NewFlagSet("encrypt", flag.ExitOnError)
	value := fs.String("value", "", "plaintext to encrypt (required)")
	_ = fs.Parse(args)
	if *value == "" {
		fatal("--value is required")
	}
	key := []byte(os.Getenv("PSP_SECRET_KEY"))
	if len(key) == 0 {
		fatal("PSP_SECRET_KEY env var is required (and must be 16/24/32 bytes)")
	}
	out, err := crypto.EncryptString(key, *value)
	if err != nil {
		fatal("encrypt: %v", err)
	}
	fmt.Printf("enc:%s\n", out)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
