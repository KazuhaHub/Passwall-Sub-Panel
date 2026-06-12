package handler

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// overridableScopeKeys is the set of setting keys (type.name) a Group may
// override. v3.8.0 Phase 1 starts with the 2FA enrollment-policy group — the
// cleanest, already-cascading field set (the end-to-end vertical slice). Later
// phases extend it. Every key MUST be a live setting (drift-tested against
// mysql.KnownSettingNames); all are plain bools/ints — encrypted settings are
// never overridable and the repo rejects them regardless.
var overridableScopeKeys = map[string]bool{
	"security.require_2fa_for_staff":           true,
	"security.totp_enabled":                    true,
	"security.passkey_enabled":                 true,
	"security.passkey_passwordless":            true,
	"security.twofa_allow_email":               true,
	"security.twofa_email_resend_cooldown_sec": true,
}

func overridableScopeKeyList() []string {
	out := make([]string, 0, len(overridableScopeKeys))
	for k := range overridableScopeKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// scopeGroupLookup is the slice of ports.GroupRepo this handler needs (existence
// validation only) — interface-segregated so the test fake stays tiny.
type scopeGroupLookup interface {
	GetByID(ctx context.Context, id int64) (*domain.Group, error)
}

// AdminScopeSettingsHandler exposes per-group setting overrides under
// /api/admin/groups/:id/scope-settings. Admin-only — these are policy settings.
type AdminScopeSettingsHandler struct {
	groups scopeGroupLookup
	scope  ports.ScopeSettingsRepo
}

func NewAdminScopeSettingsHandler(groups scopeGroupLookup, scope ports.ScopeSettingsRepo) *AdminScopeSettingsHandler {
	return &AdminScopeSettingsHandler{groups: groups, scope: scope}
}

type scopeSettingsDTO struct {
	ScopeType   string            `json:"scope_type"`
	ScopeID     int64             `json:"scope_id"`
	Overridable []string          `json:"overridable"`
	Overrides   map[string]string `json:"overrides"` // "type.name" -> raw KV value
}

type setScopeOverrideRequest struct {
	Type  string `json:"type" binding:"required"`
	Name  string `json:"name" binding:"required"`
	Value string `json:"value"`
}

// Get returns the group's sparse overrides + the overridable-key catalog. The
// frontend overlays `overrides` (keyed "type.name") onto the global settings it
// already holds to render inherit / overridden state per field.
func (h *AdminScopeSettingsHandler) Get(c *gin.Context) {
	id, ok := h.requireGroup(c)
	if !ok {
		return
	}
	overrides, err := h.scope.ListOverrides(c.Request.Context(), "group", id)
	if err != nil {
		respondError(c, err)
		return
	}
	m := make(map[string]string, len(overrides))
	for _, o := range overrides {
		m[o.Type+"."+o.Name] = o.Value
	}
	c.JSON(http.StatusOK, scopeSettingsDTO{
		ScopeType:   "group",
		ScopeID:     id,
		Overridable: overridableScopeKeyList(),
		Overrides:   m,
	})
}

// SetOverride upserts one per-group override. The key must be in the overridable
// allowlist; the repo additionally rejects unknown / encrypted keys.
func (h *AdminScopeSettingsHandler) SetOverride(c *gin.Context) {
	id, ok := h.requireGroup(c)
	if !ok {
		return
	}
	var req setScopeOverrideRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !overridableScopeKeys[req.Type+"."+req.Name] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "setting " + req.Type + "." + req.Name + " is not overridable per group"})
		return
	}
	if err := h.scope.SetOverride(c.Request.Context(), "group", id, ports.ScopeOverride{Type: req.Type, Name: req.Name, Value: req.Value}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteOverride removes one override = restore inheritance for that key.
func (h *AdminScopeSettingsHandler) DeleteOverride(c *gin.Context) {
	id, ok := h.requireGroup(c)
	if !ok {
		return
	}
	if err := h.scope.DeleteOverride(c.Request.Context(), "group", id, c.Param("type"), c.Param("name")); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// requireGroup parses :id and verifies the group exists. On failure it writes the
// response itself and returns ok=false so the caller returns immediately.
func (h *AdminScopeSettingsHandler) requireGroup(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return 0, false
	}
	if _, err := h.groups.GetByID(c.Request.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		} else {
			respondError(c, err)
		}
		return 0, false
	}
	return id, true
}
