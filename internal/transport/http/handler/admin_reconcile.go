package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/service/reconcile"
)

type AdminReconcileHandler struct {
	recon *reconcile.Service
}

func NewAdminReconcileHandler(recon *reconcile.Service) *AdminReconcileHandler {
	return &AdminReconcileHandler{recon: recon}
}

func (h *AdminReconcileHandler) Run(c *gin.Context) {
	report, err := h.recon.RunOnce(c.Request.Context(), reconcile.LevelFull)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}
