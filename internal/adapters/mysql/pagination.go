package mysql

import (
	"strings"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// inferTotalOrCount returns the total row count for a paged query
// while skipping the COUNT round-trip when it's safe to infer.
//
// The fast path: when admin is on page 1 AND the Find returned fewer
// rows than the page would hold, we know with certainty that no more
// rows exist beyond the visible set — total = len(rows). This
// short-circuits the LIKE %x%-driven COUNT scan on big tables (audit /
// sub_logs / mail_sent) where the COUNT is otherwise the slowest query
// in the request.
//
// The slow path (page > 1, or page 1 fully populated) still runs the
// real COUNT against the pre-pagination query. countQuery is the same
// *gorm.DB that built the WHERE clauses but BEFORE ORDER/LIMIT/OFFSET
// were appended — see callers; the standard idiom is to pass
// `q.Session(&gorm.Session{})` of the WHERE-only query to applyPagination
// while keeping the original `q` reference for this Count call.
func inferTotalOrCount(countQuery *gorm.DB, p ports.Pagination, returnedRows int) (int64, error) {
	if p.Page <= 1 && p.PageSize > 0 && returnedRows < p.PageSize {
		return int64(returnedRows), nil
	}
	var total int64
	if err := countQuery.Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// applyPagination wires a ports.Pagination onto a GORM query in the
// standardized way every ListPaged implementation uses:
//   - SortBy is consulted against sortAllowlist; unknown values fall
//     back to defaultSort. This is the SQL-injection guard — admin
//     input never reaches `ORDER BY` directly.
//   - SortDir is lower-cased and clamped to "asc" or "desc"
//     (anything else → "asc").
//   - Page/PageSize are clamped to sane bounds (page >= 1, size in
//     [1, 200]). PageSize == 0 is treated as "no slice" so internal
//     callers can pass a zero-value Pagination and still get every
//     row in the keyword scope.
//
// Returns the query with sort + limit + offset applied. Caller is
// responsible for building the WHERE clause (keyword + typed
// predicates) before calling this — passing a pre-narrowed query
// makes the Count below cheap and accurate.
func applyPagination(q *gorm.DB, p ports.Pagination, sortAllowlist map[string]string, defaultSort string) *gorm.DB {
	col, ok := sortAllowlist[strings.ToLower(p.SortBy)]
	if !ok {
		col = defaultSort
	}
	dir := strings.ToLower(strings.TrimSpace(p.SortDir))
	if dir != "desc" {
		dir = "asc"
	}
	q = q.Order(col + " " + dir)
	if p.PageSize > 0 {
		size := p.PageSize
		if size > 200 {
			size = 200
		}
		page := p.Page
		if page < 1 {
			page = 1
		}
		q = q.Limit(size).Offset((page - 1) * size)
	}
	return q
}

// likeEscaper neutralises every LIKE meta-character (`\`, `%`, `_`) so an
// admin / operator typing "100_" or "50%" into a search box matches the
// literal substring instead of "any single char after 100" / "any
// suffix after 50". Backslash MUST come first or the subsequent escapes
// double up. The pattern is fed into a parameterised query (no string
// concatenation into SQL), so this isn't an injection fix — it's a
// "user-facing search behaves the way users expect" fix, AND a perf
// guard against the wildcard `_` pattern triggering a full-table scan
// when the column is otherwise selective.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// keywordLike returns the LIKE pattern for a Pagination.Keyword in the
// canonical form every repo uses: leading-and-trailing `%` for
// substring match, LIKE wildcards escaped, lowercased so the matching
// query can do `LOWER(col) LIKE ?` (works on every backend including
// Postgres where bare LIKE is case-sensitive). Empty input → empty
// output; callers should branch on that to avoid stapling a meaningless
// "%%" predicate onto every query.
func keywordLike(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	return "%" + likeEscaper.Replace(strings.ToLower(k)) + "%"
}

// likeCols builds the case-insensitive substring-match predicate every
// keyword search uses, OR-joined across the given column expressions, each
// with the `ESCAPE '\'` clause that makes keywordLike's backslash-escaping
// actually take effect. This is REQUIRED on SQLite (the zero-config default
// backend): SQLite has no default LIKE escape character, so without an
// explicit ESCAPE the backslash keywordLike injects is treated as a literal
// pattern char while `%`/`_` stay wildcards — a search for "u_5@x.org"
// silently matches nothing / the wrong rows. MySQL and Postgres default to
// backslash-as-escape, so the clause is a harmless no-op there.
//
// Each expr is wrapped as LOWER(<expr>) LIKE ? ESCAPE '\', so callers pass
// the inner column or expression (e.g. "upn" or "COALESCE(users.upn, ”)")
// and supply one bound keywordLike value per expr.
func likeCols(exprs ...string) string {
	parts := make([]string, len(exprs))
	for i, e := range exprs {
		parts[i] = "LOWER(" + e + ") LIKE ? ESCAPE '\\'"
	}
	return strings.Join(parts, " OR ")
}
