package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

// ---- lists ----

type destListView struct {
	ID              int64               `json:"id"`
	Name            string              `json:"name"`
	Kind            domain.DestListKind `json:"kind"`
	Side            domain.DestListSide `json:"side"`
	SourceURL       string              `json:"source_url,omitempty"`
	GeositeCategory string              `json:"geosite_category,omitempty"`
	GeositeAttrs    []string            `json:"geosite_attrs,omitempty"`
	EntryCount      int                 `json:"entry_count"`
	RegexpCount     int                 `json:"regexp_count"`
	// State is ready, refreshing, failed or pending.
	State         string               `json:"state"`
	Empty         bool                 `json:"empty"`
	LastFetchedAt *int64               `json:"last_fetched_at"`
	LastError     string               `json:"last_error"`
	OwnerGroupID  int64                `json:"owner_group_id"`
	UpdatedAt     int64                `json:"updated_at"`
	UsedBy        []destlist.PolicyRef `json:"used_by"`
}

func listState(l *domain.DestList, refreshing bool) string {
	switch {
	case refreshing:
		return "refreshing"
	case l.LastError != "":
		return "failed"
	case !l.Ready():
		return "pending"
	}
	return "ready"
}

func (h *AdminDestHandler) listView(l *domain.DestList, used map[int64][]destlist.PolicyRef) destListView {
	v := destListView{
		ID: l.ID, Name: l.Name, Kind: l.Kind, Side: l.Side, SourceURL: l.SourceURL, GeositeCategory: l.GeositeCategory,
		EntryCount: l.EntryCount, RegexpCount: l.RegexpCount, State: listState(l, h.w.Lists.Refreshing(l.ID)),
		Empty: l.EntryCount == 0, LastFetchedAt: msPtr(l.LastFetchedAt), LastError: l.LastError,
		OwnerGroupID: l.OwnerGroupID, UpdatedAt: msOf(l.UpdatedAt), UsedBy: used[l.ID],
	}
	if v.UsedBy == nil {
		v.UsedBy = []destlist.PolicyRef{}
	}
	if l.GeositeAttrs != "" {
		v.GeositeAttrs = strings.Split(l.GeositeAttrs, ",")
	}
	return v
}

// ListLists answers GET /api/admin/dest/lists.
func (h *AdminDestHandler) ListLists(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil && h.w.Admin != nil) {
		return
	}
	ctx := c.Request.Context()
	lists, err := h.w.Lists.Lists(ctx)
	if err != nil {
		respondError(c, err)
		return
	}
	used, err := h.w.Lists.UsedBy(ctx)
	if err != nil {
		respondError(c, err)
		return
	}
	budget, err := h.w.Admin.Budget(ctx, nil)
	if err != nil {
		respondError(c, err)
		return
	}
	items := make([]destListView, 0, len(lists))
	for _, l := range lists {
		items = append(items, h.listView(l, used))
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items, "refresh_hours": int(h.w.Lists.RefreshInterval(ctx).Hours()), "budget": budget,
	})
}

type destListRequest struct {
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	Side            string   `json:"side"`
	SourceURL       string   `json:"source_url"`
	Text            *string  `json:"text"`
	GeositeCategory string   `json:"geosite_category"`
	GeositeAttrs    []string `json:"geosite_attrs"`
	UpdatedAt       *int64   `json:"updated_at"`
}

func previewEntries(p *destlist.Parsed) []string {
	return splitLines(p.Entries, 50)
}

// PreviewList answers POST /api/admin/dest/lists/preview: the parse report and
// the first 50 canonical entries of what the body would create. NOTHING IS
// WRITTEN and the route is not audited (middleware.shouldAuditPath): the page
// calls it whenever typing pauses, and a custom list's text may be large.
func (h *AdminDestHandler) PreviewList(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil) {
		return
	}
	var req destListRequest
	if !bindDestJSON(c, &req) {
		return
	}
	side := domain.DestListSide(req.Side)
	if side == "" {
		side = domain.DestSideDest
	}
	switch domain.DestListKind(req.Kind) {
	case domain.DestListCustom:
		text := ""
		if req.Text != nil {
			text = *req.Text
		}
		p, err := destlist.ParseCustom([]byte(text), side)
		if err != nil {
			h.destError(c, err, errCodes{})
			return
		}
		c.JSON(http.StatusOK, gin.H{"report": p.Report, "entries": previewEntries(p), "entry_count": p.EntryCount, "regexp_count": p.RegexpCount})
	case domain.DestListRemote:
		pv, err := h.w.Lists.PreviewRemote(c.Request.Context(), req.SourceURL, side)
		if err != nil {
			h.destError(c, err, errCodes{})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"report": pv.Parsed.Report, "entries": previewEntries(pv.Parsed), "entry_count": pv.Parsed.EntryCount,
			"regexp_count": pv.Parsed.RegexpCount, "http_status": pv.HTTPStatus, "bytes": pv.Bytes,
		})
	case domain.DestListGeosite:
		spec, err := h.w.Lists.GeositeListSpec(c.Request.Context(), req.GeositeCategory, req.GeositeAttrs)
		if err != nil {
			h.destError(c, err, errCodes{})
			return
		}
		c.JSON(http.StatusOK, gin.H{"report": nil, "entries": splitLines(spec.Entries, 50), "entry_count": spec.EntryCount, "regexp_count": spec.RegexpCount})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind is custom, remote or geosite", "code": "dest_bad_request", "field": "kind"})
	}
}

// CreateList answers POST /api/admin/dest/lists.
func (h *AdminDestHandler) CreateList(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil) {
		return
	}
	var req destListRequest
	if !bindDestJSON(c, &req) {
		return
	}
	ctx := c.Request.Context()
	side := domain.DestListSide(req.Side)
	var (
		l      *domain.DestList
		report *destlist.ParseReport
		err    error
	)
	switch domain.DestListKind(req.Kind) {
	case domain.DestListCustom:
		text := ""
		if req.Text != nil {
			text = *req.Text
		}
		l, report, err = h.w.Lists.CreateCustom(ctx, req.Name, side, []byte(text))
	case domain.DestListRemote:
		l, report, err = h.w.Lists.CreateRemote(ctx, req.Name, req.SourceURL, side)
	case domain.DestListGeosite:
		if side == domain.DestSideSource {
			c.JSON(http.StatusBadRequest, gin.H{"error": "a geosite list is a destination list", "code": "dest_bad_request", "field": "side"})
			return
		}
		l, err = h.w.Lists.CreateGeosite(ctx, req.GeositeCategory, req.GeositeAttrs)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind is custom, remote or geosite", "code": "dest_bad_request", "field": "kind"})
		return
	}
	if err != nil {
		h.destError(c, err, errCodes{exists: "dest_name_taken"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"list": h.listView(l, nil), "report": report})
}

// GetList answers GET /api/admin/dest/lists/:id: the statistics and the first
// 200 canonical entries, never the whole list. ?text=1 adds a custom list's
// pasted text for the editor.
func (h *AdminDestHandler) GetList(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	l, err := h.w.Lists.GetList(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	used, err := h.w.Lists.UsedBy(ctx)
	if err != nil {
		respondError(c, err)
		return
	}
	out := gin.H{"list": h.listView(l, used), "samples": splitLines(l.Entries, 200)}
	if c.Query("text") == "1" && l.Kind == domain.DestListCustom {
		out["source_text"] = string(l.SourceText)
	}
	c.JSON(http.StatusOK, out)
}

// UpdateList answers PUT /api/admin/dest/lists/:id. A custom list is rewritten
// from its text; a remote list is renamed and, when its URL changed, fetched
// again; a geosite list can only be renamed away from nothing (its name is
// its category) and is refused. The request carries updated_at, and a list that
// changed since is 409 dest_list_stale.
func (h *AdminDestHandler) UpdateList(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req destListRequest
	if !bindDestJSON(c, &req) {
		return
	}
	ctx := c.Request.Context()
	cur, err := h.w.Lists.GetList(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if req.UpdatedAt != nil && *req.UpdatedAt != msOf(cur.UpdatedAt) {
		h.destError(c, domain.ErrConflict, errCodes{stale: "dest_list_stale"})
		return
	}
	var (
		l      *domain.DestList
		report *destlist.ParseReport
	)
	switch cur.Kind {
	case domain.DestListCustom:
		text := string(cur.SourceText)
		if req.Text != nil {
			text = *req.Text
		}
		l, report, err = h.w.Lists.UpdateCustom(ctx, id, req.Name, []byte(text))
	case domain.DestListRemote:
		url := req.SourceURL
		if url == "" {
			url = cur.SourceURL
		}
		l, report, err = h.w.Lists.UpdateRemote(ctx, id, req.Name, url)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "a geosite list is not edited", "code": "dest_bad_request", "field": "kind"})
		return
	}
	if err != nil {
		h.destError(c, err, errCodes{exists: "dest_name_taken", stale: "dest_list_stale"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"list": h.listView(l, nil), "report": report})
}

// DeleteList answers DELETE /api/admin/dest/lists/:id. A list a policy uses is
// 409 dest_list_in_use, with who.
func (h *AdminDestHandler) DeleteList(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.w.Lists.DeleteList(c.Request.Context(), id); err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type destEntriesRequest struct {
	Add    []string `json:"add"`
	Remove []string `json:"remove"`
}

// ListEntries answers POST /api/admin/dest/lists/:id/entries: lines added to
// and removed from a custom list's text in one write.
func (h *AdminDestHandler) ListEntries(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req destEntriesRequest
	if !bindDestJSON(c, &req) {
		return
	}
	l, report, err := h.w.Lists.AddEntries(c.Request.Context(), id, req.Add, req.Remove)
	if err != nil {
		h.destError(c, err, errCodes{})
		return
	}
	c.JSON(http.StatusOK, gin.H{"list": h.listView(l, nil), "report": report})
}

// RefreshList answers POST /api/admin/dest/lists/:id/refresh: 202, the refresh
// runs in the background and the state comes back with the list.
func (h *AdminDestHandler) RefreshList(c *gin.Context) {
	if !h.ready(c, h.w.Lists != nil) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	ctx := c.Request.Context()
	l, err := h.w.Lists.GetList(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	if l.Kind != domain.DestListRemote {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only a remote list is refreshed", "code": "dest_bad_request", "field": "kind"})
		return
	}
	if h.w.Lists.BeginRefresh(id) {
		work := func(bg context.Context) {
			defer h.w.Lists.EndRefresh(id)
			if _, err := h.w.Lists.RefreshList(bg, id); err != nil && !errors.Is(err, domain.ErrNotFound) {
				log.Warn("destlist: refresh failed", "list_id", id, "err", err)
			}
		}
		if h.w.Async != nil {
			h.w.Async.Go("dest-list-refresh-now", work)
		} else {
			work(ctx)
		}
	}
	c.JSON(http.StatusAccepted, gin.H{"state": "refreshing"})
}
