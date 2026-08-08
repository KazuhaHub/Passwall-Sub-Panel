// Package upnnorm implements `psp normalize-upn`, the one-shot backfill that
// rewrites stored login names to their canonical form.
//
// It is the R2 step. R1 made every WRITE canonical and taught GetByUPN to probe
// exact-then-normalized, which covered every direction except one: a row stored
// as `Alice@Corp.Com` cannot be reached by the canonical spelling
// `alice@corp.com`, because both probes compare an input against the stored
// column and neither can rewrite storage.
//
// Deliberately a subcommand rather than a boot migration: folding can violate
// users.upn's unique index on an install that already holds a near-duplicate
// pair, and neither aborting at boot nor silently rewriting an admin's login
// name is an acceptable thing for a panel to do while coming up.
package upnnorm

import (
	"flag"
	"fmt"
	"os"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

// Run executes the subcommand and returns the process exit code.
//
// Exit codes: 0 success (including "nothing to do"), 1 a real failure, 2 usage.
// A run that REFUSED colliding groups still exits 0 — it did exactly what it
// promised; the collisions are reported for a human to resolve.
func Run(args []string) int {
	fs := flag.NewFlagSet("normalize-upn", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: psp normalize-upn [--apply] [--config=<path>]\n\n")
		fmt.Fprintf(os.Stderr, "Rewrites stored login names (users.upn) to their canonical lower-cased,\n")
		fmt.Fprintf(os.Stderr, "trimmed form, so a canonical spelling reaches accounts created before the\n")
		fmt.Fprintf(os.Stderr, "panel normalized on write.\n\n")
		fmt.Fprintf(os.Stderr, "DRY RUN BY DEFAULT — nothing is written unless you pass --apply.\n\n")
		fmt.Fprintf(os.Stderr, "Accounts whose names collide once folded are REPORTED AND SKIPPED, never\n")
		fmt.Fprintf(os.Stderr, "merged: each row owns its own subscription token, quota and traffic\n")
		fmt.Fprintf(os.Stderr, "history, so choosing which survives is yours to make, not this tool's.\n\n")
		fmt.Fprintf(os.Stderr, "Stop the panel first on SQLite (write locking), and back up first on any\n")
		fmt.Fprintf(os.Stderr, "backend — this rewrites the column users log in with.\n\n")
		fs.PrintDefaults()
	}
	apply := fs.Bool("apply", false, "actually write the changes (default: dry run, report only)")
	cfgPath := fs.String("config", "", "path to config.yaml (default: the same resolution the panel uses)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Resolve the DB from config.yaml rather than asking for a DSN: this tool
	// rewrites the login column, and pointing it at the wrong database by
	// mistyping a DSN is a failure mode worth designing out.
	cfg, err := config.LoadOrGenerate(config.ResolvePath(*cfgPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: load config: %v\n", err)
		return 1
	}
	db, err := sqlstore.Open(cfg.DBKind(), cfg.DBDSN())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: open %s db: %v\n", cfg.DBKind(), err)
		return 1
	}
	if sqlDB, derr := db.DB(); derr == nil {
		defer sqlDB.Close()
	}

	rep, err := sqlstore.NormalizeStoredUPNs(db, *apply)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}

	fmt.Printf("database: %s\n", cfg.DBKind())
	fmt.Printf("scanned:  %d account(s)\n\n", rep.Scanned)

	if len(rep.Collisions) > 0 {
		fmt.Printf("SKIPPED — %d login name(s) are held by more than one account:\n", len(rep.Collisions))
		for _, c := range rep.Collisions {
			fmt.Printf("  %-40s %d accounts\n", c.Folded, c.N)
		}
		fmt.Printf("\n  These are NOT folded and were left untouched. Each account keeps its own\n")
		fmt.Printf("  subscription token, quota and traffic history, and each remains reachable\n")
		fmt.Printf("  by its exact spelling. Decide which one survives, remove the other in the\n")
		fmt.Printf("  admin UI, then re-run this command.\n\n")
	}

	if rep.Changed == 0 {
		if len(rep.Collisions) == 0 {
			fmt.Println("Nothing to do — every login name is already canonical.")
		} else {
			fmt.Println("Nothing to fold outside the skipped groups above.")
		}
		return 0
	}

	verb := "would rewrite"
	if rep.Applied {
		verb = "rewrote"
	}
	fmt.Printf("%s %d account(s):\n", verb, rep.Changed)
	for _, rn := range rep.Renames {
		fmt.Printf("  id=%-6d %q -> %q\n", rn.ID, rn.From, rn.To)
	}

	if !rep.Applied {
		fmt.Printf("\nDry run — nothing was written. Re-run with --apply to make these changes.\n")
		fmt.Printf("Tell affected users their login name is now the lower-cased form; their\n")
		fmt.Printf("passwords, subscriptions and traffic history are untouched.\n")
	} else {
		fmt.Printf("\nDone. Affected users must now log in with the lower-cased spelling;\n")
		fmt.Printf("passwords, subscription tokens and traffic history are unchanged.\n")
	}
	return 0
}
