package domain

import "strings"

// NormalizeUPN is the ONE definition of "the same login username" in this
// codebase. Every write that persists a upn and every guard that checks for a
// collision must run its own argument through it — a guard that is only correct
// when its caller remembered to normalize is not a guard.
//
// WHY THIS EXISTS AT ALL: `users.upn` is a plain varchar(255) UNIQUE with no
// COLLATE, and the three supported backends disagree about what that index
// means. MySQL's default utf8mb4 collation folds case (and, on
// utf8mb4_0900_ai_ci, accents too), so the index refuses `Alice` when `alice`
// exists and the panel got that protection for free. Postgres and SQLite
// compare byte-exact, so on those two — SQLite being the DEFAULT backend —
// nothing folds and `Alice` and `alice` become two separate identities for one
// human. Normalizing here, in Go, is what makes the existing plain index
// enforce the same invariant on all three dialects with no per-dialect DDL.
//
// It also closes a race the service-layer guards cannot: read-then-insert is
// TOCTOU-racy, and once both writers normalize to the same string the unique
// index settles the race for us.
//
// TrimSpace matters as much as ToLower and is the less obvious half. Go's
// TrimSpace strips space, \t, \n, \v, \f, \r, U+0085 and U+00A0 — an unbounded
// set of variants that all resolve to the same account but, before this, keyed
// the login-attempt counter differently, letting `" admin"` sidestep the
// lockout and captcha gates that `"admin"` had tripped.
//
// Deliberately simple: ToLower, not ToLowerSpecial — the latter is
// locale-dependent and would make identity vary by server locale, which is the
// class of bug this function exists to kill. Unicode NFC normalization is OUT
// OF SCOPE: composed and decomposed forms of the same accented name remain
// distinct identities here. That is a known, accepted limitation, not an
// oversight; revisit it only with a migration plan, since changing this
// function's output silently re-partitions every existing account.
func NormalizeUPN(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
