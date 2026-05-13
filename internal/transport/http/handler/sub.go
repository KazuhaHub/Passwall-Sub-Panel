package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/render"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// SubHandler serves GET /sub/:token — the public subscription endpoint.
type SubHandler struct {
	user    *user.Service
	render  *render.Service
	subLogs ports.SubLogRepo
}

func NewSubHandler(userSvc *user.Service, renderSvc *render.Service, subLogs ports.SubLogRepo) *SubHandler {
	return &SubHandler{user: userSvc, render: renderSvc, subLogs: subLogs}
}

func (h *SubHandler) Get(c *gin.Context) {
	token := c.Param("token")
	u, err := h.user.GetBySubToken(c.Request.Context(), token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.String(http.StatusNotFound, "")
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !u.Enabled {
		c.String(http.StatusForbidden, "disabled")
		return
	}
	if u.IsExpired(time.Now()) {
		c.String(http.StatusForbidden, "expired")
		return
	}

	// TODO(M1): pick clientType from query param ?client= or UA detection.
	out, err := h.render.RenderForUser(c.Request.Context(), u, domain.ClientMihomo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Best-effort access log.
	_ = h.subLogs.Insert(c.Request.Context(), &domain.SubLog{
		UserID:     u.ID,
		IP:         c.ClientIP(),
		UA:         c.GetHeader("User-Agent"),
		ClientType: "mihomo",
		AccessedAt: time.Now(),
	})

	for k, v := range out.Headers {
		c.Header(k, v)
	}
	c.Data(http.StatusOK, out.ContentType, out.Body)
}
