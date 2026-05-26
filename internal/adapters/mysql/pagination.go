package mysql

import (
	"strings"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

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

// keywordLike returns the LIKE pattern for a Pagination.Keyword in the
// same lowercase / wildcard form every repo uses. Empty input → empty
// output; callers should branch on that to avoid stapling a meaningless
// "%%" predicate onto every query.
func keywordLike(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	return "%" + strings.ToLower(k) + "%"
}
