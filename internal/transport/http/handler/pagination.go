package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// parsePagination reads the shared list-endpoint query params:
//
//	?page=1&page_size=25&keyword=foo&sort_by=col&sort_dir=asc
//
// Clamps page >= 1, page_size in [1, 200] with a default of 25. Empty
// keyword / sort_by carry through to the repo, which decides how to
// interpret "no sort" (each repo allowlists its sortable columns).
// Returns a ports.Pagination ready to drop into any Filter struct.
func parsePagination(c *gin.Context) ports.Pagination {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(c.Query("page_size"))
	if size < 1 {
		size = 25
	} else if size > 200 {
		size = 200
	}
	return ports.Pagination{
		Page:     page,
		PageSize: size,
		Keyword:  c.Query("keyword"),
		SortBy:   c.Query("sort_by"),
		SortDir:  c.Query("sort_dir"),
	}
}

// pagedEnvelope wraps a paged result in the standardized response shape
// every list endpoint returns:
//
//	{ "items": [...], "total": N, "page": P, "page_size": S }
//
// Callers pass the items DTO slice (any type), the repo-reported total,
// and the Pagination they used. last_page is computed client-side as
// ceil(total / page_size) to avoid duplicating that math in every
// handler.
func pagedEnvelope(items any, total int64, p ports.Pagination) gin.H {
	return gin.H{
		"items":     items,
		"total":     total,
		"page":      p.Page,
		"page_size": p.PageSize,
	}
}
