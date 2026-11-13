package handler

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// AdminDestSettingsHandler serves the access-control page's settings
// (/api/admin/dest/settings): the dest_* keys (ports.AccessControlSettings)
// with the shipped default of every number and the value the panel runs with.
// adminGroup only, like every access-control route; the PUT is audited by
// AuditWrites like any write under /api/admin.
//
// It is the one writer of those keys, by the arrangement the risk center's
// policy page established: the settings store saves whole records only, so the
// PUT loads the whole record, changes this part and saves the whole record,
// under uiSettingsWriteMu — the lock the system settings PUT takes around its
// own load → save, or the two would write each other's part back as it was.
type AdminDestSettingsHandler struct {
	repo ports.SettingsRepo
	// defaults is what the record is loaded with: the system settings page's,
	// because a save here writes the WHOLE record (see AdminRiskPolicyHandler).
	defaults func() ports.UISettings
}

// NewAdminDestSettingsHandler serves the settings out of repo.
func NewAdminDestSettingsHandler(repo ports.SettingsRepo) *AdminDestSettingsHandler {
	return &AdminDestSettingsHandler{repo: repo, defaults: (&AdminSettingsHandler{}).defaults}
}

type destSettingsView struct {
	// Settings are the stored values as stored: 0 and "" mean unset.
	Settings ports.AccessControlSettings `json:"settings"`
	// Defaults is the shipped value of every numeric key, for the empty
	// field's placeholder.
	Defaults map[string]float64 `json:"defaults"`
	// Effective is what the panel runs with for the stored values.
	Effective ports.AccessControlEffective `json:"effective"`
}

func destSettingsViewOf(s ports.UISettings) destSettingsView {
	return destSettingsView{
		Settings:  s.AccessControlSettings(),
		Defaults:  ports.AccessControlSettingsDefaults(),
		Effective: ports.AccessControlEffectiveOf(s),
	}
}

// Get answers the stored settings with their defaults and effective values.
func (h *AdminDestSettingsHandler) Get(c *gin.Context) {
	s, err := h.repo.Load(c.Request.Context(), h.defaults())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, destSettingsViewOf(s))
}

type destSettingsRequest struct {
	// Settings stays raw so it can be decoded onto the STORED settings: a key
	// the body leaves out keeps its stored value. The page sends only the keys
	// it changed, so a tab opened before another admin's save cannot revert
	// that save by saving one field of its own.
	Settings json.RawMessage `json:"settings"`
}

// Put merges the sent keys onto the stored settings and saves. Nothing is
// half-applied: a body without a settings object, a key that does not decode
// (an unknown one included: a typo that was silently ignored would be a
// setting that lies) or a value out of range refuses the whole save.
func (h *AdminDestSettingsHandler) Put(c *gin.Context) {
	var req destSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if raw := bytes.TrimSpace(req.Settings); len(raw) == 0 || raw[0] != '{' {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Settings must be an object of dest_ keys"})
		return
	}

	uiSettingsWriteMu.Lock()
	defer uiSettingsWriteMu.Unlock()

	prev, err := h.repo.Load(c.Request.Context(), h.defaults())
	if err != nil {
		respondError(c, err)
		return
	}
	next := prev.AccessControlSettings()
	dec := json.NewDecoder(bytes.NewReader(req.Settings))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&next); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if field, err := ports.ValidateAccessControlSettings(next); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "field": field, "code": "dest_settings_invalid"})
		return
	}
	saved := prev
	saved.SetAccessControlSettings(next)
	if err := h.repo.Save(c.Request.Context(), saved); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, destSettingsViewOf(saved))
}
