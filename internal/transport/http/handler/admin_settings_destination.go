package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/gin-gonic/gin"
)

func destinationSettingsFrom(s ports.UISettings) domain.DestinationSettings {
	return s.DestinationSettings()
}

type destinationSettingsPatch struct {
	DestHitRetentionDays      *int `json:"dest_hit_retention_days"`
	DestTrialRetentionDays    *int `json:"dest_trial_retention_days"`
	DestUsageRetentionDays    *int `json:"dest_usage_retention_days"`
	DestListRefreshHours      *int `json:"dest_list_refresh_hours"`
	DestPolicyApplyMinSeconds *int `json:"dest_policy_apply_min_seconds"`
}

func resolveDestinationSettings(req destinationSettingsPatch, previous ports.UISettings) (domain.DestinationSettings, error) {
	value := destinationSettingsFrom(previous)
	if req.DestHitRetentionDays != nil {
		value.HitRetentionDays = *req.DestHitRetentionDays
	}
	if req.DestTrialRetentionDays != nil {
		value.TrialRetentionDays = *req.DestTrialRetentionDays
	}
	if req.DestUsageRetentionDays != nil {
		value.UsageRetentionDays = *req.DestUsageRetentionDays
	}
	if req.DestListRefreshHours != nil {
		value.ListRefreshHours = *req.DestListRefreshHours
	}
	if req.DestPolicyApplyMinSeconds != nil {
		value.PolicyApplyMinSeconds = *req.DestPolicyApplyMinSeconds
	}
	return value, value.Validate()
}

type AdminDestinationSettingsHandler struct {
	repo           ports.SettingsRepo
	refreshChanged func()
}

func NewAdminDestinationSettingsHandler(repo ports.SettingsRepo, refreshChanged func()) *AdminDestinationSettingsHandler {
	return &AdminDestinationSettingsHandler{repo: repo, refreshChanged: refreshChanged}
}

func destinationView(value domain.DestinationSettings) gin.H {
	toDTO := func(value domain.DestinationSettings) ports.AccessControlSettings {
		var s ports.UISettings
		s.SetDestinationSettings(value)
		return s.AccessControlSettings()
	}
	effective := value.Effective()
	return gin.H{"settings": toDTO(value), "defaults": toDTO(domain.DefaultDestinationSettings()), "effective": toDTO(effective)}
}

func (h *AdminDestinationSettingsHandler) Get(c *gin.Context) {
	if h.repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "destination settings unavailable"})
		return
	}
	s, err := h.repo.Load(c.Request.Context(), (&AdminSettingsHandler{}).defaults())
	if err != nil {
		respondError(c, err)
		return
	}
	value := destinationSettingsFrom(s)
	c.JSON(http.StatusOK, destinationView(value))
}

func (h *AdminDestinationSettingsHandler) Put(c *gin.Context) {
	if h.repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "destination settings unavailable"})
		return
	}
	var request struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.Settings == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid destination settings document"})
		return
	}
	var patch destinationSettingsPatch
	targets := map[string]**int{"dest_hit_retention_days": &patch.DestHitRetentionDays, "dest_trial_retention_days": &patch.DestTrialRetentionDays, "dest_usage_retention_days": &patch.DestUsageRetentionDays, "dest_list_refresh_hours": &patch.DestListRefreshHours, "dest_policy_apply_min_seconds": &patch.DestPolicyApplyMinSeconds}
	for key, raw := range request.Settings {
		target, known := targets[key]
		if !known {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown destination setting", "errors": map[string]string{key: "unknown setting"}})
			return
		}
		if err := json.Unmarshal(raw, target); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid destination setting", "errors": map[string]string{key: "expected integer"}})
			return
		}
	}
	// All three settings writers serialize their complete load/change/save.
	uiSettingsWriteMu.Lock()
	defer uiSettingsWriteMu.Unlock()
	previous, err := h.repo.Load(c.Request.Context(), (&AdminSettingsHandler{}).defaults())
	if err != nil {
		respondError(c, err)
		return
	}
	value, err := resolveDestinationSettings(patch, previous)
	if err != nil {
		fieldErrors := map[string]string{}
		for _, key := range []string{"dest_hit_retention_days", "dest_trial_retention_days", "dest_usage_retention_days", "dest_list_refresh_hours", "dest_policy_apply_min_seconds"} {
			if strings.Contains(err.Error(), key) {
				fieldErrors[key] = err.Error()
			}
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "errors": fieldErrors})
		return
	}
	if value == destinationSettingsFrom(previous) {
		c.JSON(http.StatusOK, destinationView(value))
		return
	}
	updated := previous
	updated.SetDestinationSettings(value)
	if err := h.repo.Save(c.Request.Context(), updated); err != nil {
		respondError(c, err)
		return
	}
	if value.ListRefreshHours != destinationSettingsFrom(previous).ListRefreshHours && h.refreshChanged != nil {
		h.refreshChanged()
	}
	c.JSON(http.StatusOK, destinationView(value))
}
