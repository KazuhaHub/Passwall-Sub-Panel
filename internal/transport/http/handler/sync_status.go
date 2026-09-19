package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

// syncStatusUnavailableCode is the stable code a client keys on when the read
// could not be answered. It is deliberately distinct from an empty result:
// "state unknown" and "nothing pending" drive different UI.
const syncStatusUnavailableCode = "sync_status_unavailable"

type syncTaskViewDTO struct {
	ID         int64  `json:"id"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Attempts   int    `json:"attempts"`
	NextRunAt  string `json:"next_run_at"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	// HasError reports only that an error was recorded. The error text itself is
	// never exposed — it can quote an upstream endpoint or a token.
	HasError bool `json:"has_error"`
}

type syncStatusDTO struct {
	TargetType   string `json:"target_type"`
	TargetID     int64  `json:"target_id"`
	TargetExists bool   `json:"target_exists"`
	// ObservedAt is when the server read its own task store. It says nothing
	// about when — or whether — the upstream panel applied anything.
	ObservedAt       string            `json:"observed_at"`
	CoveredTaskTypes []string          `json:"covered_task_types"`
	State            string            `json:"state"`
	ActiveTasks      []syncTaskViewDTO `json:"active_tasks"`
	// Truncated flags mean the lists are capped, not that the counts are exact.
	ActiveTasksTruncated bool              `json:"active_tasks_truncated"`
	RecentTerminalTasks  []syncTaskViewDTO `json:"recent_terminal_tasks"`
	HistoryTruncated     bool              `json:"history_truncated"`
	// HistoryScope is always retained_only: purged rows are simply absent, and
	// absence must not be read as "never ran".
	HistoryScope string `json:"history_scope"`
}

func syncTaskViewToDTO(v user.SyncTaskView) syncTaskViewDTO {
	out := syncTaskViewDTO{
		ID: v.ID, Type: string(v.Type), Status: string(v.Status),
		Attempts: v.Attempts, HasError: v.HasError,
		NextRunAt: v.NextRunAt.UTC().Format(time.RFC3339),
		CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: v.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if v.FinishedAt != nil {
		out.FinishedAt = v.FinishedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func syncStatusToDTO(s *user.SyncStatus) syncStatusDTO {
	types := make([]string, len(s.CoveredTaskTypes))
	for i, t := range s.CoveredTaskTypes {
		types[i] = string(t)
	}
	active := make([]syncTaskViewDTO, 0, len(s.ActiveTasks))
	for _, v := range s.ActiveTasks {
		active = append(active, syncTaskViewToDTO(v))
	}
	terminal := make([]syncTaskViewDTO, 0, len(s.RecentTerminalTasks))
	for _, v := range s.RecentTerminalTasks {
		terminal = append(terminal, syncTaskViewToDTO(v))
	}
	return syncStatusDTO{
		TargetType: s.TargetType, TargetID: s.TargetID, TargetExists: s.TargetExists,
		ObservedAt:       s.ObservedAt.UTC().Format(time.RFC3339),
		CoveredTaskTypes: types, State: string(s.State),
		ActiveTasks:          active,
		ActiveTasksTruncated: s.ActiveTasksTruncated,
		RecentTerminalTasks:  terminal,
		HistoryTruncated:     s.HistoryTruncated,
		HistoryScope:         s.HistoryScope,
	}
}

// writeSyncStatusUnavailable answers 503 with the stable code. Used for every
// path where the read could not be completed — a handler that answered 200 with
// no_active_tasks here would be reproducing the HasPendingSync defect.
func writeSyncStatusUnavailable(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error": "Sync status is temporarily unavailable",
		"code":  syncStatusUnavailableCode,
	})
}

// GetSyncStatus serves GET /api/admin/users/:id/sync-status.
//
// Authorization is role-shaped rather than uniform, because "may I look at this
// user" and "may I look at a user who no longer exists" are different questions:
//
//   - admin    — any ID, including one whose row is gone (target_exists=false,
//     retained tasks still visible: that is how a deletion is inspected)
//   - operator — only a target that still exists AND has role=user; an
//     admin/operator target is 403, a missing one 404
//
// The operator lookup fails closed: an error there is 503, never a pass.
func (h *AdminUserHandler) GetSyncStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}

	if claims.Role == domain.RoleOperator {
		target, lookupErr := h.user.Get(c.Request.Context(), id)
		switch {
		case errors.Is(lookupErr, domain.ErrNotFound):
			// An operator may not observe a target that is gone; only an admin
			// can inspect what a deleted user left behind.
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		case lookupErr != nil:
			writeSyncStatusUnavailable(c)
			return
		case target.Role != domain.RoleUser:
			c.JSON(http.StatusForbidden, gin.H{"error": "Operators cannot view admin or operator accounts"})
			return
		}
	}

	status, err := h.user.SyncStatus(c.Request.Context(), id)
	if err != nil {
		// Includes ErrUnavailable and anything else the read could not answer.
		writeSyncStatusUnavailable(c)
		return
	}
	c.JSON(http.StatusOK, syncStatusToDTO(status))
}

// SyncStatus serves GET /api/user/me/sync-status.
//
// The target is taken from the authenticated identity and never from the
// request, so there is no target to authorize beyond being signed in — the same
// shape as the other self-service reads.
func (h *UserMeHandler) SyncStatus(c *gin.Context) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No auth"})
		return
	}
	status, err := h.user.SyncStatus(c.Request.Context(), claims.UserID)
	if err != nil {
		writeSyncStatusUnavailable(c)
		return
	}
	c.JSON(http.StatusOK, syncStatusToDTO(status))
}
