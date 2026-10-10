package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

// JSON can expand each UTF-8 byte into a six-byte escape. The parser still
// enforces the decoded four-MiB source limit; routing admits that worst case.
const DestinationListJSONLimit = 6*destlist.MaxCustomBytes + 64<<10

type DestinationListOverview struct {
	Lists        []domain.DestList
	UsedBy       map[int64][]domain.DestReference
	RefreshHours int
	Budget       destpolicy.Budget
}

type AdminDestinationListsHandler struct {
	lists    *destlist.Service
	overview func(context.Context) (DestinationListOverview, error)
	dispatch func(string, func(context.Context))
}

func (h *AdminDestinationListsHandler) SetOverview(read func(context.Context) (DestinationListOverview, error)) {
	h.overview = read
}
func (h *AdminDestinationListsHandler) SetDispatcher(dispatch func(string, func(context.Context))) {
	h.dispatch = dispatch
}

func NewAdminDestinationListsHandler(lists *destlist.Service) *AdminDestinationListsHandler {
	return &AdminDestinationListsHandler{lists: lists}
}

type destinationListInput struct {
	Name      string              `json:"name"`
	Kind      domain.DestListKind `json:"kind"`
	SourceURL string              `json:"source_url"`
	Category  string              `json:"geosite_category"`
	Attrs     string              `json:"geosite_attrs"`
	Text      string              `json:"text"`
	UpdatedAt *int64              `json:"updated_at"`
}

func decodeDestinationList(c *gin.Context, update bool) (domain.DestList, time.Time, bool) {
	var req destinationListInput
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var trailing any
	if err := decoder.Decode(&req); err != nil {
		c.JSON(400, gin.H{"error": "dest_list_parse_failed"})
		return domain.DestList{}, time.Time{}, false
	}
	if err := decoder.Decode(&trailing); err != io.EOF {
		c.JSON(400, gin.H{"error": "dest_list_parse_failed"})
		return domain.DestList{}, time.Time{}, false
	}
	field := ""
	if strings.TrimSpace(req.Name) == "" || utf8.RuneCountInString(req.Name) > 128 {
		field = "name"
	}
	switch req.Kind {
	case domain.DestListCustom:
		if req.SourceURL != "" || req.Category != "" || req.Attrs != "" {
			field = "kind"
		}
	case domain.DestListRemote:
		if req.SourceURL == "" || len(req.SourceURL) > 1024 || req.Category != "" || req.Attrs != "" || req.Text != "" {
			field = "source_url"
		}
	case domain.DestListGeosite:
		if req.Category == "" || len(req.Category) > 128 || len(req.Attrs) > 128 || req.SourceURL != "" || req.Text != "" {
			field = "geosite_category"
		}
	default:
		field = "kind"
	}
	if update && (req.UpdatedAt == nil || *req.UpdatedAt <= 0) || !update && req.UpdatedAt != nil {
		field = "updated_at"
	}
	if field != "" {
		c.JSON(400, gin.H{"error": "dest_list_parse_failed", "field": field})
		return domain.DestList{}, time.Time{}, false
	}
	expected := time.Time{}
	if req.UpdatedAt != nil {
		expected = time.UnixMilli(*req.UpdatedAt).UTC()
	}
	return domain.DestList{Name: req.Name, Kind: req.Kind, SourceURL: req.SourceURL, GeositeCategory: req.Category, GeositeAttrs: req.Attrs, SourceText: []byte(req.Text)}, expected, true
}

func destinationListID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(400, gin.H{"error": "dest_list_parse_failed", "field": "id"})
		return 0, false
	}
	return id, true
}

func destinationListError(c *gin.Context, err error) {
	var used *domain.DestListInUseError
	var parse *destlist.Error
	var definition *destpolicy.DefinitionError
	var fetch *destlist.FetchError
	switch {
	case errors.As(err, &used):
		c.JSON(409, gin.H{"error": "dest_list_in_use", "used_by": used.UsedBy})
	case errors.As(err, &definition):
		if definition.Detail.Kind == "invalid" {
			c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": definition.Detail.Field})
		} else {
			c.JSON(400, gin.H{"error": "dest_policy_over_limit", "kind": definition.Detail.Kind, "used": definition.Detail.Used, "limit": definition.Detail.Limit})
		}
	case errors.As(err, &parse):
		code := parse.Code
		if code == "broad_entry" {
			code = "dest_list_too_broad"
		}
		c.JSON(400, gin.H{"error": code, "bad": []gin.H{{"line": parse.Line, "entry": parse.Entry}}})
	case errors.As(err, &fetch):
		c.JSON(503, gin.H{"error": "dest_list_fetch_failed", "http_status": fetch.Status})
	case errors.Is(err, domain.ErrConflict) && strings.Contains(err.Error(), "dest_list_stale"):
		c.JSON(409, gin.H{"error": "dest_list_stale"})
	case errors.Is(err, domain.ErrValidation) && strings.Contains(err.Error(), "dest_list_too_broad"):
		c.JSON(400, gin.H{"error": "dest_list_too_broad"})
	default:
		respondPublicError(c, err)
	}
}

func destinationTime(value *time.Time) *int64 {
	if value == nil {
		return nil
	}
	v := value.UnixMilli()
	return &v
}
func destinationEntrySamples(entries []byte, limit int) []string {
	result := make([]string, 0, limit)
	text := string(entries)
	for len(text) > 0 && len(result) < limit {
		line, rest, _ := strings.Cut(text, "\n")
		text = rest
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func destinationListView(list domain.DestList, text bool) gin.H {
	view := destinationListBaseView(list)
	view["entries"] = destinationEntrySamples(list.Entries, 200)
	view["content_sha256"] = list.ContentSHA256
	view["entry_types"] = destinationEntryTypes(list.Entries)
	if text && list.Kind == domain.DestListCustom {
		view["source_text"] = string(list.SourceText)
	}
	return view
}
func destinationListBaseView(list domain.DestList) gin.H {
	state := "ready"
	if list.Kind != domain.DestListCustom && list.LastFetchedAt == nil {
		state = "pending"
	}
	if list.LastError != "" {
		state = "failed"
	}
	return gin.H{"id": list.ID, "name": list.Name, "kind": list.Kind, "source_url": list.SourceURL, "geosite_category": list.GeositeCategory, "geosite_attrs": list.GeositeAttrs, "entry_count": list.EntryCount, "regexp_count": list.RegexpCount, "state": state, "last_fetched_at": destinationTime(list.LastFetchedAt), "last_error": list.LastError, "parse_report": list.ParseReport, "owner_group_id": list.OwnerGroupID, "updated_at": list.UpdatedAt.UnixMilli()}
}
func destinationEntryTypes(entries []byte) map[string]int {
	counts := map[string]int{"domain": 0, "full": 0, "keyword": 0, "regexp": 0, "cidr": 0}
	text := string(entries)
	for text != "" {
		line, rest, _ := strings.Cut(text, "\n")
		text = rest
		if line == "" {
			continue
		}
		kind, _, _ := strings.Cut(line, ":")
		if _, known := counts[kind]; !known || kind == "cidr" {
			kind = "cidr"
		}
		counts[kind]++
	}
	return counts
}

func (h *AdminDestinationListsHandler) available(c *gin.Context) bool {
	if h.lists == nil {
		c.JSON(503, gin.H{"error": "destination lists unavailable"})
		return false
	}
	return true
}
func (h *AdminDestinationListsHandler) Preview(c *gin.Context) {
	if !h.available(c) {
		return
	}
	list, _, ok := decodeDestinationList(c, false)
	if !ok {
		return
	}
	result, err := h.lists.Preview(c.Request.Context(), list)
	if err != nil {
		destinationListError(c, err)
		return
	}
	c.JSON(200, gin.H{"parse_report": result.Parsed.Report, "entries": destinationEntrySamples(result.Parsed.Entries, 50), "content_sha256": result.Parsed.ContentSHA256, "entry_count": result.Parsed.EntryCount, "regexp_count": result.Parsed.RegexpCount, "http_status": result.HTTPStatus, "bytes": result.Bytes})
}
func (h *AdminDestinationListsHandler) Create(c *gin.Context) {
	if !h.available(c) {
		return
	}
	list, expected, ok := decodeDestinationList(c, false)
	if !ok {
		return
	}
	if err := h.lists.Save(c.Request.Context(), &list, expected); err != nil {
		destinationListError(c, err)
		return
	}
	c.JSON(http.StatusCreated, destinationListView(list, false))
}
func (h *AdminDestinationListsHandler) Put(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := destinationListID(c)
	if !ok {
		return
	}
	list, expected, ok := decodeDestinationList(c, true)
	if !ok {
		return
	}
	list.ID = id
	if err := h.lists.Save(c.Request.Context(), &list, expected); err != nil {
		destinationListError(c, err)
		return
	}
	c.JSON(200, destinationListView(list, false))
}
func (h *AdminDestinationListsHandler) Get(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := destinationListID(c)
	if !ok {
		return
	}
	list, err := h.lists.Get(c.Request.Context(), id)
	if err != nil {
		destinationListError(c, err)
		return
	}
	view := destinationListView(list, c.Query("text") == "1")
	if h.lists.IsRefreshing(id) {
		view["state"] = "refreshing"
	}
	c.JSON(200, view)
}
func (h *AdminDestinationListsHandler) Delete(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := destinationListID(c)
	if !ok {
		return
	}
	if err := h.lists.Delete(c.Request.Context(), id); err != nil {
		destinationListError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AdminDestinationListsHandler) Entries(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := destinationListID(c)
	if !ok {
		return
	}
	var input struct {
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var trailing any
	if err := decoder.Decode(&input); err != nil {
		c.JSON(400, gin.H{"error": "dest_list_parse_failed"})
		return
	}
	if err := decoder.Decode(&trailing); err != io.EOF {
		c.JSON(400, gin.H{"error": "dest_list_parse_failed"})
		return
	}
	list, err := h.lists.Entries(c.Request.Context(), id, input.Add, input.Remove)
	if err != nil {
		destinationListError(c, err)
		return
	}
	c.JSON(200, destinationListView(list, false))
}

func (h *AdminDestinationListsHandler) List(c *gin.Context) {
	if !h.available(c) {
		return
	}
	if h.overview == nil {
		c.JSON(503, gin.H{"error": "destination lists unavailable"})
		return
	}
	view, err := h.overview(c.Request.Context())
	if err != nil {
		destinationListError(c, err)
		return
	}
	items := make([]gin.H, 0, len(view.Lists))
	for _, list := range view.Lists {
		item := destinationListBaseView(list)
		delete(item, "parse_report")
		if list.ParseReport == nil {
			item["parse_report_summary"] = nil
		} else {
			item["parse_report_summary"] = gin.H{"accepted": list.ParseReport.Accepted, "ignored": list.ParseReport.Ignored, "ignored_broad": list.ParseReport.IgnoredBroad, "rewritten": list.ParseReport.Rewritten}
		}
		if h.lists.IsRefreshing(list.ID) {
			item["state"] = "refreshing"
		}
		used := view.UsedBy[list.ID]
		if used == nil {
			used = []domain.DestReference{}
		}
		item["used_by"] = used
		items = append(items, item)
	}
	c.JSON(200, gin.H{"items": items, "refresh_hours": view.RefreshHours, "budget": view.Budget})
}

func (h *AdminDestinationListsHandler) Refresh(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, ok := destinationListID(c)
	if !ok {
		return
	}
	if err := h.lists.QueueRefresh(c.Request.Context(), id, h.dispatch); err != nil {
		destinationListError(c, err)
		return
	}
	c.Status(http.StatusAccepted)
}

func (h *AdminDestinationListsHandler) Categories(c *gin.Context) {
	if !h.available(c) {
		return
	}
	categories, at, state, err := h.lists.CategoryView(c.Request.Context())
	if err != nil {
		if errors.Is(err, domain.ErrUnavailable) {
			c.JSON(503, gin.H{"error": "dest_geosite_unavailable", "refreshing": state.Refreshing, "last_error": state.LastError})
		} else {
			destinationListError(c, err)
		}
		return
	}
	c.JSON(200, gin.H{"categories": categories, "updated_at": at.UnixMilli(), "refreshing": state.Refreshing, "last_error": state.LastError})
}

func (h *AdminDestinationListsHandler) RefreshCategories(c *gin.Context) {
	if !h.available(c) {
		return
	}
	if err := h.lists.QueueCategoryRefresh(c.Request.Context(), h.dispatch); err != nil {
		if errors.Is(err, domain.ErrUnavailable) {
			c.JSON(503, gin.H{"error": "dest_geosite_unavailable"})
		} else {
			destinationListError(c, err)
		}
		return
	}
	c.Status(http.StatusAccepted)
}
