package handler

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

type AdminDestinationGroupPreviewHandler struct {
	read func(context.Context, int64, []int64) (destpolicy.GroupAccessPreview, error)
}

func NewAdminDestinationGroupPreviewHandler(read func(context.Context, int64, []int64) (destpolicy.GroupAccessPreview, error)) *AdminDestinationGroupPreviewHandler {
	return &AdminDestinationGroupPreviewHandler{read: read}
}

func (h *AdminDestinationGroupPreviewHandler) Get(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	groupID, err := strconv.ParseInt(c.Param("group_id"), 10, 64)
	if err != nil || groupID <= 0 || strconv.FormatInt(groupID, 10) != c.Param("group_id") {
		destinationHitQueryError(c)
		return
	}
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		destinationHitQueryError(c)
		return
	}
	ids := []int64{}
	for key, entries := range values {
		if key != "list_ids" || len(entries) != 1 {
			destinationHitQueryError(c)
			return
		}
		if entries[0] == "" {
			continue
		}
		for _, raw := range strings.Split(entries[0], ",") {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
				destinationHitQueryError(c)
				return
			}
			ids = append(ids, id)
		}
	}
	if h.read == nil {
		respondPublicError(c, domain.ErrUnavailable)
		return
	}
	preview, err := h.read(c.Request.Context(), groupID, ids)
	if errors.Is(err, domain.ErrNotFound) {
		c.JSON(404, gin.H{"error": "dest_group_not_found"})
		return
	}
	if errors.Is(err, domain.ErrValidation) {
		destinationHitQueryError(c)
		return
	}
	if err != nil {
		respondPublicError(c, domain.ErrUnavailable)
		return
	}
	c.JSON(200, preview)
}
