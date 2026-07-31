package sqlstore

import (
	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// AuditUPNCanonicalization reports, at boot, the two states that keep a install
// from having exactly one identity per login name. It WARNS and never fails —
// mirrors AuditSecretsAtRest, and for the same reason: refusing to start over a
// data condition the admin has not been told about yet is worse than the
// condition.
//
// It exists because normalization can only fix the FUTURE. Writes now
// canonicalize the upn, so no new near-duplicates appear, but rows written
// before that (CreateLocal stored what an admin typed; EnsureSSO stored the
// IdP's assertion verbatim) are still there. This is the prerequisite for the
// one-shot backfill: an operator cannot know whether folding existing rows is
// safe until they know whether any two rows would collide when folded.
//
//	COLLIDING  — two or more rows share a LOWER(upn). Folding these would
//	             violate the unique index, so the backfill must refuse to touch
//	             them and a human has to decide which account survives (whose
//	             sub token, whose quota, whose traffic history). Until then both
//	             rows keep working and each is reachable only by its exact
//	             spelling.
//	NON-CANONICAL — a row whose upn != NormalizeUPN(upn). These are safe to fold
//	             and are exactly the rows that still depend on GetByUPN's
//	             legacy exact-match probe; that probe cannot be retired until
//	             this count reaches zero everywhere.
//
// LOWER() here is fine: this runs once at boot over a table of users, not on
// the login hot path. It is also why the login path deliberately does NOT use
// LOWER() — see GetByUPN.
func AuditUPNCanonicalization(db *gorm.DB) {
	if db == nil || !db.Migrator().HasTable("users") {
		return
	}

	var collisions []struct {
		Folded string
		N      int64
	}
	if err := db.Raw(
		`SELECT LOWER(upn) AS folded, COUNT(*) AS n FROM users GROUP BY LOWER(upn) HAVING COUNT(*) > 1`,
	).Scan(&collisions).Error; err != nil {
		// A read failure here must never be fatal — it is a diagnostic.
		log.Warn("upn audit: could not check for colliding usernames", "err", err)
		return
	}
	for _, c := range collisions {
		log.Warn("upn audit: multiple accounts share one login name (case-insensitively)",
			"upn_folded", c.Folded, "rows", c.N,
			"impact", "each row is reachable only by its exact spelling; SSO linking, password recovery and quota apply per-row",
			"action", "decide which account survives and remove the other; automated backfill will refuse to merge these")
	}

	var upns []string
	if err := db.Raw(`SELECT upn FROM users`).Scan(&upns).Error; err != nil {
		log.Warn("upn audit: could not check for non-canonical usernames", "err", err)
		return
	}
	var nonCanonical int
	var sample string
	for _, u := range upns {
		if u != domain.NormalizeUPN(u) {
			nonCanonical++
			if sample == "" {
				sample = u
			}
		}
	}
	if nonCanonical > 0 {
		log.Warn("upn audit: accounts stored with a non-canonical login name",
			"rows", nonCanonical, "example", sample,
			"impact", "these still log in normally via the legacy exact-match lookup, but a canonical spelling of the same name will not reach them",
			"action", "they can be folded safely once no colliding pairs remain")
	}
}
