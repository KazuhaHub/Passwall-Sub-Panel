package sqlstore

import (
	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// upnCollision is one group of rows that share a login name once folded.
type upnCollision struct {
	Folded string
	N      int64
}

// auditUPNState is the query half of the boot audit, split out so the
// diagnostic's ACCURACY is testable on every dialect rather than only its
// not-crashing. Returns the colliding folded groups and how many rows are
// stored non-canonically.
//
// A missing users table is reported as "nothing to audit" (nil, 0, nil): a very
// old install runs `psp migrate` first, and the audit runs unconditionally at
// boot before anyone knows which schema state they are in.
//
// It detects that from the QUERY ERROR, deliberately not from
// db.Migrator().HasTable. ownership_repo.go documents why: GORM's HasTable
// swallows ALL errors and returns false, so gating on its bool means one
// transient blip (SQLITE_BUSY, pool exhaustion) silently skips the audit — and
// a diagnostic that skips itself on a bad day is worse than no diagnostic,
// because an operator reads its silence as "clean". Any error that is NOT
// positively "table absent" is returned so the caller can say so out loud.
func auditUPNState(db *gorm.DB) ([]upnCollision, int, error) {
	if db == nil {
		return nil, 0, nil
	}

	var collisions []upnCollision
	if err := db.Raw(
		`SELECT LOWER(upn) AS folded, COUNT(*) AS n FROM users GROUP BY LOWER(upn) HAVING COUNT(*) > 1`,
	).Scan(&collisions).Error; err != nil {
		if isMissingTableErr(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}

	// Folding is compared in Go rather than as `WHERE upn <> LOWER(upn)` so the
	// comparison is byte-for-byte the one NormalizeUPN performs — SQL LOWER()
	// is locale/collation dependent and would disagree with it on exactly the
	// rows that matter, and no SQL expression trims the way TrimSpace does.
	var upns []string
	if err := db.Raw(`SELECT upn FROM users`).Scan(&upns).Error; err != nil {
		if isMissingTableErr(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	var nonCanonical int
	for _, u := range upns {
		if u != domain.NormalizeUPN(u) {
			nonCanonical++
		}
	}
	return collisions, nonCanonical, nil
}

// AuditUPNCanonicalization reports, at boot, the two states that keep an install
// from having exactly one identity per login name. It WARNS and never fails —
// mirrors AuditSecretsAtRest, and for the same reason: refusing to start over a
// data condition the admin has not been told about yet is worse than the
// condition.
//
// It exists because normalization can only fix the FUTURE. Writes now
// canonicalize the upn, so no new near-duplicates appear, but rows written
// before that (CreateLocal stored what an admin typed; EnsureSSO stored the
// IdP's assertion verbatim) are still there. This is the prerequisite for
// `psp normalize-upn`: an operator cannot know whether folding existing rows is
// safe until they know whether any two rows would collide when folded.
//
//	COLLIDING  — two or more rows share a LOWER(upn). Folding these would
//	             violate the unique index, so the backfill refuses to touch them
//	             and a human has to decide which account survives (whose sub
//	             token, whose quota, whose traffic history). Until then both rows
//	             keep working, each reachable only by its exact spelling.
//	NON-CANONICAL — a row whose upn != NormalizeUPN(upn). Safe to fold, and
//	             exactly the rows that still depend on GetByUPN's legacy
//	             exact-match probe; that probe cannot be retired until this
//	             count reaches zero everywhere.
func AuditUPNCanonicalization(db *gorm.DB) {
	collisions, nonCanonical, err := auditUPNState(db)
	if err != nil {
		// Say so rather than passing silently: a failed audit and a clean one
		// must never look the same to an operator reading the log.
		log.Warn("upn audit: could not inspect login names", "err", err,
			"impact", "this boot produced NO all-clear; do not read the absence of further warnings as clean")
		return
	}

	for _, c := range collisions {
		log.Warn("upn audit: multiple accounts share one login name (case-insensitively)",
			"upn_folded", c.Folded, "rows", c.N,
			"impact", "each row is reachable only by its exact spelling; SSO linking, password recovery and quota apply per-row",
			"action", "decide which account survives and remove the other; `psp normalize-upn` will refuse to merge these")
	}
	if nonCanonical > 0 {
		log.Warn("upn audit: accounts stored with a non-canonical login name",
			"rows", nonCanonical,
			"impact", "these still log in normally via the legacy exact-match lookup, but a canonical spelling of the same name will not reach them",
			"action", "run `psp normalize-upn --dry-run` to preview, then `--apply` to fold them")
	}
}
