package handler

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/gin-gonic/gin"
)

type AdminDestinationHitsHandler struct {
	read func(context.Context, domain.DestHitQuery) (domain.DestHitPage, error)
}

func NewAdminDestinationHitsHandler(read func(context.Context, domain.DestHitQuery) (domain.DestHitPage, error)) *AdminDestinationHitsHandler {
	return &AdminDestinationHitsHandler{read: read}
}

func (h *AdminDestinationHitsHandler) Get(c *gin.Context) {
	if h.read == nil {
		c.JSON(503, gin.H{"error": "destination records unavailable"})
		return
	}
	q, err := destinationHitQuery(c.Request.URL.RawQuery, time.Now().UTC())
	if err != nil {
		destinationHitQueryError(c)
		return
	}
	page, err := h.read(c.Request.Context(), q)
	if errors.Is(err, domain.ErrValidation) {
		destinationHitQueryError(c)
		return
	}
	if err != nil {
		// A read dependency failure is unavailable, never an empty history.
		// Do not pass driver details to the shared diagnostic logger: they may
		// contain the private destination or account used by this query.
		respondPublicError(c, domain.ErrUnavailable)
		return
	}
	var items any = page.Records
	if page.GroupBy != "none" {
		items = page.Groups
	}
	c.JSON(200, gin.H{"items": items, "group_by": page.GroupBy, "total": page.Total, "page": page.Page, "page_size": page.PageSize,
		"summary": page.Summary, "sources": page.Sources, "dropped_in_range": page.DroppedInRange, "losses": page.Losses})
}

func destinationHitQueryError(c *gin.Context) {
	c.JSON(400, gin.H{"error": "dest_audit_query_invalid"})
}

func destinationHitQuery(raw string, now time.Time) (domain.DestHitQuery, error) {
	q := domain.DestHitQuery{Since: now.Add(-24 * time.Hour), Until: now, Page: 1, PageSize: 50}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return q, domain.ErrValidation
	}
	allowed := map[string]bool{"user_id": true, "panel_id": true, "source": true, "source_kind": true, "action": true, "since": true, "until": true, "include_trial": true, "q": true, "group_by": true, "page": true, "page_size": true}
	for key, items := range values {
		if !allowed[key] || len(items) != 1 {
			return q, domain.ErrValidation
		}
	}
	for key, target := range map[string]*int64{"user_id": &q.UserID, "panel_id": &q.PanelID} {
		if value, ok := values[key]; ok {
			id, err := strconv.ParseInt(value[0], 10, 64)
			if err != nil || id <= 0 {
				return q, domain.ErrValidation
			}
			*target = id
		}
	}
	for key, target := range map[string]*int{"page": &q.Page, "page_size": &q.PageSize} {
		if value, ok := values[key]; ok {
			n, err := strconv.Atoi(value[0])
			if err != nil || n <= 0 {
				return q, domain.ErrValidation
			}
			*target = n
		}
	}
	if value, ok := values["include_trial"]; ok {
		switch value[0] {
		case "1", "true":
			q.IncludeTrial = true
		case "0", "false":
		default:
			return q, domain.ErrValidation
		}
	}
	if value, ok := values["until"]; ok {
		q.Until, err = destinationHitTime(value[0], now, false)
		if err != nil {
			return q, err
		}
		q.Since = q.Until.Add(-24 * time.Hour)
	}
	if value, ok := values["since"]; ok {
		q.Since, err = destinationHitTime(value[0], q.Until, true)
		if err != nil {
			return q, err
		}
	}
	q.Source, q.SourceKind, q.Action, q.Keyword, q.GroupBy = values.Get("source"), values.Get("source_kind"), values.Get("action"), values.Get("q"), values.Get("group_by")
	return q, nil
}

func destinationHitTime(raw string, base time.Time, relative bool) (time.Time, error) {
	if relative {
		switch raw {
		case "1h":
			return base.Add(-time.Hour), nil
		case "24h":
			return base.Add(-24 * time.Hour), nil
		}
		if strings.HasSuffix(raw, "d") {
			n, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
			if err == nil && n >= 1 && n <= 31 && strconv.Itoa(n)+"d" == raw {
				return base.Add(-time.Duration(n) * 24 * time.Hour), nil
			}
			return time.Time{}, domain.ErrValidation
		}
	}
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms > 0 {
		return time.UnixMilli(ms).UTC(), nil
	}
	if instant, err := time.Parse(time.RFC3339Nano, raw); err == nil && instant.UnixMilli() > 0 {
		return instant.UTC(), nil
	}
	return time.Time{}, domain.ErrValidation
}
