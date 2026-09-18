package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodediagnostics"
)

// Remote diagnostics are administrator-only, INCLUDING the result.
//
// A diagnostic is the most detailed picture of a machine this panel can produce
// without a shell, and section 13.5 returns it to administrators alone. These
// routes live in the admin group, which is what carries that; a future move of
// them into staffGroup is what would have to answer for it.

type NodeDiagnosticsService interface {
	Create(context.Context, int64, nodediagnostics.Request) (*nodediagnostics.Status, bool, error)
	Get(context.Context, int64, string) (*nodediagnostics.Status, error)
}

func (h *AdminServersHandler) WithNodeDiagnostics(service NodeDiagnosticsService) *AdminServersHandler {
	h.nodeDiagnostics = service
	return h
}

// diagnosticRequest is the PANEL's request shape, and it deliberately omits
// schema_version: that field versions the NODE wire, and the panel is what fills
// it in. Accepting it from a browser would let a caller claim a version this
// build may not speak.
type diagnosticRequest struct {
	Sections  []string `json:"sections"`
	MaxEvents *int     `json:"max_events"`
}

// RequestNodeDiagnostics records the intent to collect, or returns the
// collection already running for this server.
func (h *AdminServersHandler) RequestNodeDiagnostics(c *gin.Context) {
	id, ok := h.nodeDiagnosticsServerID(c)
	if !ok {
		return
	}
	var body diagnosticRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid diagnostics request"})
		return
	}
	request := nodediagnostics.Request{
		SchemaVersion: nodeprotocol.DiagnosticsSchemaVersion,
		Sections:      body.Sections,
	}
	if body.MaxEvents != nil {
		request.MaxEvents = *body.MaxEvents
	}
	status, created, err := h.nodeDiagnostics.Create(c.Request.Context(), id, request)
	if err != nil {
		nodeDiagnosticsError(c, err)
		return
	}
	h.auditDiagnosticRequest(c, id, status)
	// 202 ONLY WHEN ONE WAS ACTUALLY MINTED. A merged request returns the
	// running collection, and answering "accepted" for it would tell the caller
	// that something started when nothing did.
	code := http.StatusOK
	if created {
		code = http.StatusAccepted
	}
	c.JSON(code, status)
}

// GetNodeDiagnostics returns the state of one collection.
func (h *AdminServersHandler) GetNodeDiagnostics(c *gin.Context) {
	id, ok := h.nodeDiagnosticsServerID(c)
	if !ok {
		return
	}
	status, err := h.nodeDiagnostics.Get(c.Request.Context(), id, c.Param("task_id"))
	if err != nil {
		nodeDiagnosticsError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, status)
}

func (h *AdminServersHandler) nodeDiagnosticsServerID(c *gin.Context) (int64, bool) {
	privateNodeResponse(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid server ID"})
		return 0, false
	}
	if h.nodeDiagnostics == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "remote diagnostics are unavailable"})
		return 0, false
	}
	return id, true
}

// auditDiagnosticRequest records WHO ASKED FOR WHICH MACHINE, and nothing else.
//
// THE RESULT BODY IS DELIBERATELY ABSENT. An audit trail is a record of intent;
// copying the diagnostic into it would put the same data in a second place with
// a different, longer retention than the task it came from, and section 13.5
// says so directly.
func (h *AdminServersHandler) auditDiagnosticRequest(c *gin.Context, panelID int64, status *nodediagnostics.Status) {
	if h.audit == nil || status == nil {
		return
	}
	_ = h.audit.Insert(c.Request.Context(), &domain.AuditEntry{
		Actor:  actorFromGin(c),
		Action: "node_diagnostics_requested",
		Target: "server=" + strconv.FormatInt(panelID, 10) + " task=" + status.TaskID,
		IP:     c.ClientIP(),
		At:     time.Now(),
	})
}

// nodeDiagnosticsError keeps the panel's meanings apart: a request the node
// contract forbids is the caller's mistake, and a missing task is not a server
// fault.
func nodeDiagnosticsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrValidation):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "code": "node_diagnostics_invalid"})
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such diagnostic task for this server"})
	case errors.Is(err, domain.ErrResourceExhausted):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "native agent task quota is full"})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cannot process remote diagnostics"})
	}
}
