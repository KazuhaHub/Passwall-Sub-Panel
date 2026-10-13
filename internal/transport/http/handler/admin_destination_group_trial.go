package handler

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

type AdminDestinationGroupTrialHandler struct {
	read func(context.Context, int64, int) (destpolicy.GroupTrialReport, error)
}

func NewAdminDestinationGroupTrialHandler(read func(context.Context, int64, int) (destpolicy.GroupTrialReport, error)) *AdminDestinationGroupTrialHandler {
	return &AdminDestinationGroupTrialHandler{read: read}
}

func (h *AdminDestinationGroupTrialHandler) Get(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	groupID, err := strconv.ParseInt(c.Param("group_id"), 10, 64)
	if err != nil || groupID <= 0 || strconv.FormatInt(groupID, 10) != c.Param("group_id") {
		destinationHitQueryError(c)
		return
	}
	days := 7
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		destinationHitQueryError(c)
		return
	}
	for key, entries := range values {
		if key != "days" || len(entries) != 1 {
			destinationHitQueryError(c)
			return
		}
		days, err = strconv.Atoi(entries[0])
		if err != nil || days <= 0 || strconv.Itoa(days) != entries[0] {
			destinationHitQueryError(c)
			return
		}
	}
	if h.read == nil {
		respondPublicError(c, domain.ErrUnavailable)
		return
	}
	report, err := h.read(c.Request.Context(), groupID, days)
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
	c.JSON(200, report)
}
