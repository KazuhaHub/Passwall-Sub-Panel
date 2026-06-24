package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/reconcile"
)

// sharedHealer runs the shared-client drift heal / re-merge (implemented by
// user.Service.HealSharedClients) so the manual Reconcile button converges users
// onto the current shared-client shape, not just the per-node ownership table.
type sharedHealer interface {
	HealSharedClients(ctx context.Context) (int, error)
}

type AdminReconcileHandler struct {
	recon  *reconcile.Service
	healer sharedHealer
}

func NewAdminReconcileHandler(recon *reconcile.Service, healer sharedHealer) *AdminReconcileHandler {
	return &AdminReconcileHandler{recon: recon, healer: healer}
}

func (h *AdminReconcileHandler) Run(c *gin.Context) {
	report, err := h.recon.RunOnce(c.Request.Context(), reconcile.LevelFull)
	if err != nil {
		respondError(c, err)
		return
	}
	// Also run the shared-client heal so a manual Reconcile does the SAME work as
	// the reconcile loop: re-merge users still split into pre-merge per-class
	// clients, re-provision drifted ones, delete orphans. Best-effort — a heal
	// error doesn't undo the per-node reconcile that already succeeded.
	if h.healer != nil {
		if n, herr := h.healer.HealSharedClients(c.Request.Context()); herr != nil {
			log.Warn("admin reconcile: shared-client heal", "reconciled", n, "err", herr)
		}
	}
	c.JSON(http.StatusOK, report)
}
