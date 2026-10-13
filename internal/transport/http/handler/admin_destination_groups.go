package handler

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

type AdminDestinationGroupsHandler struct {
	save func(context.Context, int64, destpolicy.GroupModeInput, time.Time) (domain.DestGroupMode, error)
	list func(context.Context) ([]destpolicy.GroupAccessSummary, error)
	read func(context.Context, int64) (destpolicy.GroupAccessDetail, error)
}

func NewAdminDestinationGroupsHandler(save func(context.Context, int64, destpolicy.GroupModeInput, time.Time) (domain.DestGroupMode, error)) *AdminDestinationGroupsHandler {
	return &AdminDestinationGroupsHandler{save: save}
}

func (h *AdminDestinationGroupsHandler) SetReaders(list func(context.Context) ([]destpolicy.GroupAccessSummary, error), read func(context.Context, int64) (destpolicy.GroupAccessDetail, error)) {
	h.list, h.read = list, read
}

func (h *AdminDestinationGroupsHandler) List(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if c.Request.URL.RawQuery != "" {
		destinationHitQueryError(c)
		return
	}
	if h.list == nil {
		respondPublicError(c, domain.ErrUnavailable)
		return
	}
	items, err := h.list(c.Request.Context())
	if err != nil {
		respondPublicError(c, domain.ErrUnavailable)
		return
	}
	if items == nil {
		items = []destpolicy.GroupAccessSummary{}
	}
	c.JSON(200, gin.H{"items": items})
}

func (h *AdminDestinationGroupsHandler) Get(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.ParseInt(c.Param("group_id"), 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != c.Param("group_id") || c.Request.URL.RawQuery != "" {
		destinationHitQueryError(c)
		return
	}
	if h.read == nil {
		respondPublicError(c, domain.ErrUnavailable)
		return
	}
	item, err := h.read(c.Request.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		c.JSON(404, gin.H{"error": "dest_group_not_found"})
		return
	}
	if err != nil {
		respondPublicError(c, domain.ErrUnavailable)
		return
	}
	c.JSON(200, item)
}

func destinationGroupModeView(mode domain.DestGroupMode) gin.H {
	ids := slices.Clone(mode.ListIDs)
	if ids == nil {
		ids = []int64{}
	}
	var updated int64
	if !mode.UpdatedAt.IsZero() {
		updated = mode.UpdatedAt.UnixMilli()
	}
	return gin.H{"group_id": mode.GroupID, "mode": mode.Mode, "stage": mode.Stage, "list_ids": ids, "base_list_id": mode.BaseListID, "extra_list_id": mode.ExtraListID, "stage_changed_at": destinationTime(mode.StageChangedAt), "updated_at": updated}
}

func destinationGroupSaveError(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		c.JSON(404, gin.H{"error": "dest_group_not_found"})
		return
	}
	if errors.Is(err, domain.ErrConflict) {
		for _, code := range []string{"dest_mode_stale", "dest_mode_invalid_transition", "dest_list_not_ready"} {
			if strings.Contains(err.Error(), code) {
				c.JSON(409, gin.H{"error": code})
				return
			}
		}
		respondPublicError(c, domain.ErrConflict)
		return
	}
	if errors.Is(err, domain.ErrValidation) {
		destinationPolicyError(c, err)
		return
	}
	respondPublicError(c, domain.ErrUnavailable)
}

func (h *AdminDestinationGroupsHandler) Put(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.ParseInt(c.Param("group_id"), 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != c.Param("group_id") || c.Request.URL.RawQuery != "" {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": "group_id"})
		return
	}
	var req struct {
		Mode      *string  `json:"mode"`
		Stage     *string  `json:"stage"`
		ListIDs   *[]int64 `json:"list_ids"`
		BaseHosts []string `json:"base_hosts"`
		UpdatedAt *int64   `json:"updated_at"`
	}
	if !destinationPolicyDecode(c, &req) {
		return
	}
	field := ""
	switch {
	case req.Mode == nil:
		field = "mode"
	case req.Stage == nil:
		field = "stage"
	case req.ListIDs == nil:
		field = "list_ids"
	case req.UpdatedAt == nil || *req.UpdatedAt < 0:
		field = "updated_at"
	}
	if field != "" {
		c.JSON(400, gin.H{"error": "dest_policy_invalid", "field": field})
		return
	}
	if h.save == nil {
		respondPublicError(c, domain.ErrUnavailable)
		return
	}
	var expected time.Time
	if *req.UpdatedAt != 0 {
		expected = time.UnixMilli(*req.UpdatedAt).UTC()
	}
	input := destpolicy.GroupModeInput{Mode: *req.Mode, Stage: *req.Stage, ListIDs: *req.ListIDs, BaseHosts: req.BaseHosts}
	mode, err := h.save(c.Request.Context(), id, input, expected)
	if err != nil {
		destinationGroupSaveError(c, err)
		return
	}
	c.JSON(200, destinationGroupModeView(mode))
}
