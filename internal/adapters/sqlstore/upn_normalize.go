package sqlstore

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// UPNRename is one row the backfill would rewrite, reported so an operator can
// see exactly which login strings are about to change under their users before
// anything is written.
type UPNRename struct {
	ID   int64
	From string
	To   string
}

// UPNNormalizeReport is what one backfill run did (or, in dry-run, would do).
type UPNNormalizeReport struct {
	// Scanned is every row examined, so "0 changed" can be distinguished from
	// "the table was empty / absent".
	Scanned int
	// Changed counts rows folded — or, in dry-run, rows that WOULD be folded.
	Changed int
	// Renames enumerates those rows.
	Renames []UPNRename
	// Collisions are folded groups holding more than one row. These are
	// REFUSED, never merged, and every row inside them is left untouched.
	Collisions []upnCollision
	// Applied is false for a dry run.
	Applied bool
}

// NormalizeStoredUPNs rewrites stored login names to their canonical form
// (domain.NormalizeUPN), skipping any group that would collide.
//
// This is R2 — the step R1 deliberately could not perform. R1 made every WRITE
// canonical and taught GetByUPN to probe exact-then-normalized, which fixed
// every direction except one: a row stored as `Alice@Corp.Com` cannot be reached
// by the canonical spelling `alice@corp.com`, because both probes compare an
// input against the stored column and neither can rewrite storage. Only this
// does.
//
// WHY IT IS NOT A BOOT MIGRATION. Folding can violate users.upn's unique index
// on an install that already holds a near-duplicate pair. Aborting at boot is
// unacceptable (the panel is coming up, possibly unattended), and silently
// rewriting an admin's login name is worse. So it is operator-invoked, dry-run
// by default, and refuses to guess.
//
// WHY COLLISIONS ARE REFUSED RATHER THAN MERGED. Two rows sharing a folded name
// are two real accounts: each owns a sub_token (a live subscription URL), a
// uuid (the proxy-credential derivation seed), traffic counters, a group and an
// expiry. Deciding which survives is a business decision with user-visible
// consequences, not something a migration may infer. They are reported and left
// exactly as they were — both keep working, each reachable by its exact
// spelling, precisely as before this ran.
//
// A refused group never blocks the rest of the run: unrelated rows still fold.
func NormalizeStoredUPNs(db *gorm.DB, apply bool) (UPNNormalizeReport, error) {
	rep := UPNNormalizeReport{Applied: apply}
	if db == nil {
		return rep, nil
	}

	collisions, _, err := auditUPNState(db)
	if err != nil {
		return rep, fmt.Errorf("audit upn state: %w", err)
	}
	rep.Collisions = collisions
	// Every folded name that is off-limits this run.
	blocked := make(map[string]struct{}, len(collisions))
	for _, c := range collisions {
		blocked[c.Folded] = struct{}{}
	}

	var rows []struct {
		ID  int64
		UPN string
	}
	if err := db.Raw(`SELECT id, upn FROM users ORDER BY id`).Scan(&rows).Error; err != nil {
		if isMissingTableErr(err) {
			return rep, nil
		}
		return rep, fmt.Errorf("list users: %w", err)
	}
	rep.Scanned = len(rows)

	for _, r := range rows {
		canonical := domain.NormalizeUPN(r.UPN)
		if canonical == r.UPN {
			continue // already canonical
		}
		// auditUPNState groups by SQL LOWER(); the blocked key is that same
		// LOWER() output, so compare against it rather than against
		// NormalizeUPN's result, which additionally trims.
		if _, bad := blocked[canonical]; bad {
			continue
		}
		if canonical == "" {
			// A upn that is nothing but whitespace would fold to "" and
			// collide with any other such row on a NOT NULL unique column.
			// Refuse rather than invent a name.
			continue
		}
		rep.Renames = append(rep.Renames, UPNRename{ID: r.ID, From: r.UPN, To: canonical})
	}
	rep.Changed = len(rep.Renames)

	if !apply || rep.Changed == 0 {
		return rep, nil
	}

	// One transaction: a partially-folded user table is a worse state than
	// either end, since half the rows would be reachable by a spelling the
	// other half is not.
	if err := db.Transaction(func(tx *gorm.DB) error {
		for _, rn := range rep.Renames {
			res := tx.Exec(`UPDATE users SET upn = ? WHERE id = ? AND upn = ?`, rn.To, rn.ID, rn.From)
			if res.Error != nil {
				return fmt.Errorf("fold user %d (%q -> %q): %w", rn.ID, rn.From, rn.To, res.Error)
			}
			// The upn = ? guard makes this a compare-and-swap: if the row moved
			// under us (a concurrent admin edit, another copy of this tool),
			// stop rather than overwrite someone else's write.
			if res.RowsAffected != 1 {
				return fmt.Errorf("fold user %d: expected 1 row, touched %d — the row changed underneath this run; re-run to pick up the new state",
					rn.ID, res.RowsAffected)
			}
		}
		return nil
	}); err != nil {
		return rep, err
	}
	return rep, nil
}
