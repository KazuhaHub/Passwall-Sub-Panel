package yaml

import (
	"sort"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// slicePage paginates an in-memory slice that was already loaded from
// disk + filtered + sorted by the caller. Yaml-backed repos (rule sets,
// templates) keep tiny working sets (typically <10 files), so the
// "load everything, filter in Go, slice" model is fine and avoids
// duplicating the DB-side keyword/sort plumbing.
//
// Returns the page slice plus the total count BEFORE slicing — that's
// what the API envelope needs for "Total" rendering.
func slicePage[T any](items []T, p ports.Pagination) ([]T, int64) {
	total := int64(len(items))
	if p.PageSize <= 0 {
		return items, total
	}
	size := p.PageSize
	if size > 200 {
		size = 200
	}
	page := p.Page
	if page < 1 {
		page = 1
	}
	start := (page - 1) * size
	if start >= len(items) {
		return []T{}, total
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], total
}

// keywordMatch reports whether any of the haystack strings contains the
// keyword (case-insensitive). Empty keyword always matches.
func keywordMatch(keyword string, haystacks ...string) bool {
	k := strings.ToLower(strings.TrimSpace(keyword))
	if k == "" {
		return true
	}
	for _, h := range haystacks {
		if strings.Contains(strings.ToLower(h), k) {
			return true
		}
	}
	return false
}

// sortByName sorts a slice in place using a `less` callback. Thin
// wrapper around sort.SliceStable so call sites read like
// "sortBy(items, asc, less)" instead of inlining a closure that
// branches on dir each time.
func sortBy[T any](items []T, dir string, less func(a, b T) bool) {
	if strings.ToLower(dir) == "desc" {
		sort.SliceStable(items, func(i, j int) bool { return less(items[j], items[i]) })
		return
	}
	sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
}
