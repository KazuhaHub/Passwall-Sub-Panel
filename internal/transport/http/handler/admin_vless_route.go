package handler

import (
	"net/http"
	"strconv"

	"github.com/KazuhaHub/passwall-sub-panel/internal/service/vlessroute"
	"github.com/gin-gonic/gin"
)

type AdminVlessRouteHandler struct{ service *vlessroute.Service }

func NewAdminVlessRouteHandler(service *vlessroute.Service) *AdminVlessRouteHandler {
	return &AdminVlessRouteHandler{service: service}
}
func (h *AdminVlessRouteHandler) id(c *gin.Context) (int64, bool) {
	if h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Routing line service is unavailable"})
		return 0, false
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid node id"})
		return 0, false
	}
	return id, true
}
func (h *AdminVlessRouteHandler) Get(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	view, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}
func (h *AdminVlessRouteHandler) Outbounds(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	outbounds, err := h.service.Outbounds(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": outbounds})
}
func (h *AdminVlessRouteHandler) Save(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	var req vlessroute.SaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid routing lines"})
		return
	}
	view, err := h.service.Save(c.Request.Context(), id, req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}
