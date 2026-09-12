package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodeagentupgrade"
)

type NativeAgentUpgradeService interface {
	Request(context.Context, int64, nodeagentupgrade.Request, string) (*nodeagentupgrade.Status, bool, error)
	Get(context.Context, int64, string) (*nodeagentupgrade.Status, error)
}

func (h *AdminServersHandler) WithNativeAgentUpgrade(service NativeAgentUpgradeService) *AdminServersHandler {
	h.nativeUpgrade = service
	return h
}

func (h *AdminServersHandler) UpgradeNativeAgent(c *gin.Context) {
	id, ok := h.nativeUpgradeServerID(c)
	if !ok {
		return
	}
	// Strict, bounded metadata only. Never accept a download URL, shell command,
	// credential, implicit latest release, duplicate or case-folded field names.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid bounded native upgrade request"})
		return
	}
	request, err := nodeagentupgrade.DecodeRequest(payload)
	if err != nil {
		nativeUpgradeError(c, err)
		return
	}
	status, created, err := h.nativeUpgrade.Request(c.Request.Context(), id, request, c.GetHeader("Idempotency-Key"))
	if err != nil {
		nativeUpgradeError(c, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusAccepted
	}
	c.JSON(code, status)
}

func (h *AdminServersHandler) GetNativeAgentUpgrade(c *gin.Context) {
	id, ok := h.nativeUpgradeServerID(c)
	if !ok {
		return
	}
	status, err := h.nativeUpgrade.Get(c.Request.Context(), id, c.Param("task_id"))
	if err != nil {
		nativeUpgradeError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *AdminServersHandler) nativeUpgradeServerID(c *gin.Context) (int64, bool) {
	privateNodeResponse(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid server ID"})
		return 0, false
	}
	if h.nativeUpgrade == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "native agent upgrades are unavailable"})
		return 0, false
	}
	return id, true
}

func nativeUpgradeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrValidation):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid exact-version native upgrade request"})
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "native upgrade server or task not found"})
	case errors.Is(err, domain.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "native upgrade identity or request conflicts"})
	case errors.Is(err, domain.ErrResourceExhausted):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "native agent task quota is full"})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cannot process native agent upgrade"})
	}
}
