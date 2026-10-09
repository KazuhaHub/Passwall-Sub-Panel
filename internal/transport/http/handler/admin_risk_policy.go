package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// AdminRiskPolicyHandler serves the risk center's policy page
// (/api/admin/risk-center/policy): every concurrent-location and risk-signal
// setting (ports.RiskCenterPolicy), with the shipped default of every number
// and the value each runtime knob is in effect at. adminGroup only, like
// every risk-center route: the policy decides whom the detectors accuse and
// when one suspends somebody's service. The PUT is audited by AuditWrites
// like any write under /api/admin.
//
// The settings store saves whole records only, so the PUT loads the whole
// record, changes the policy part and saves the whole record — under
// uiSettingsWriteMu, the lock the system settings PUT takes around its own
// load→save, or two saves interleaving would each write back the other's
// part as it was before (D6).
type AdminRiskPolicyHandler struct {
	repo ports.SettingsRepo
	// defaults is what the record is loaded with: the system settings
	// page's, because a save here writes the WHOLE record. Loaded with
	// none, the first policy save on an install that never saved the
	// settings page would persist an empty login mode and site title as
	// if an admin had chosen them.
	defaults func() ports.UISettings
}

// NewAdminRiskPolicyHandler serves the policy out of repo, loading it with
// the system settings page's defaults.
func NewAdminRiskPolicyHandler(repo ports.SettingsRepo) *AdminRiskPolicyHandler {
	return &AdminRiskPolicyHandler{repo: repo, defaults: (&AdminSettingsHandler{}).defaults}
}

// riskPolicyView is what the page reads, and what a save answers with.
type riskPolicyView struct {
	// Settings is the 48 stored values as stored: 0 means unset (the
	// default applies), and a value out of range is shown as typed — the
	// detectors clamp it where they read it.
	Settings ports.RiskCenterPolicy `json:"settings"`
	// Defaults is the shipped value of every numeric key
	// (ports.RiskCenterPolicyDefaults), for the empty field's placeholder
	// and the presets.
	Defaults map[string]float64 `json:"defaults"`
	// Effective is, for the 23 runtime knobs, the number the panel runs
	// with for the stored settings (ports.RuntimeEffective).
	Effective map[string]int `json:"effective"`
}

func riskPolicyViewOf(s ports.UISettings) riskPolicyView {
	effective := ports.RiskCenterRuntimeEffective(s)
	return riskPolicyView{
		Settings:  s.RiskCenterPolicy(),
		Defaults:  ports.RiskCenterPolicyDefaults(),
		Effective: effective,
	}
}

// Get answers the stored policy with its defaults and values in effect.
func (h *AdminRiskPolicyHandler) Get(c *gin.Context) {
	s, err := h.repo.Load(c.Request.Context(), h.defaults())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, riskPolicyViewOf(s))
}

// riskPolicyRequest is the PUT body. Settings stays raw so it can be
// decoded onto the STORED policy: a key the body leaves out keeps its
// stored value. The page sends only the keys it changed (D10), so a tab
// opened before another admin's save cannot revert that save by saving one
// field of its own. The view's defaults and effective maps are not read
// even when a client sends them back: they are the server's to state.
type riskPolicyRequest struct {
	Settings json.RawMessage `json:"settings"`
}

// Put merges the sent keys onto the stored policy and saves. Nothing is
// half-applied: a body without a settings object, any key that does not
// decode, or a bad ignore list refuses the whole save.
func (h *AdminRiskPolicyHandler) Put(c *gin.Context) {
	var req riskPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if raw := bytes.TrimSpace(req.Settings); len(raw) == 0 || raw[0] != '{' {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Settings must be an object of policy keys"})
		return
	}

	uiSettingsWriteMu.Lock()
	defer uiSettingsWriteMu.Unlock()

	prev, err := h.repo.Load(c.Request.Context(), h.defaults())
	if err != nil {
		respondError(c, err)
		return
	}
	pol := prev.RiskCenterPolicy()
	// json.Unmarshal fills every key it can and reports a bad one at the
	// end, so pol may be partly filled here: the error alone decides, and
	// nothing is saved.
	if err := json.Unmarshal(req.Settings, &pol); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Trimmed as the system settings page stored them, so the same text
	// typed on either page is the same value.
	pol.GeoAnomalyScope = strings.TrimSpace(pol.GeoAnomalyScope)
	pol.GeoAnomalyCoTravel = strings.TrimSpace(pol.GeoAnomalyCoTravel)
	pol.GeoAnomalyIgnoreAddresses = strings.TrimSpace(pol.GeoAnomalyIgnoreAddresses)
	// The ignore list is parsed by the SAME function the poll and the risk
	// worker use, so the page cannot save a list they would read
	// differently. Refused whole: a typo here fails OPEN — the relay the
	// admin meant to cover keeps accusing people — and nothing downstream
	// repairs it. The answer names the field and every bad entry, so the
	// page can mark that field and list the lines to fix.
	if _, err := domain.ParseGeoIgnoreList(pol.GeoAnomalyIgnoreAddresses); err != nil {
		bad := []string{}
		var listErr *domain.GeoIgnoreListError
		if errors.As(err, &listErr) && len(listErr.Bad) > 0 {
			bad = listErr.Bad
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "field": "geo_anomaly_ignore_addresses", "bad": bad})
		return
	}
	// Numbers are not validated: 0 means the default and an out-of-range
	// value is clamped where it is read (domain.GeoPolicyFromSettings,
	// RiskPolicyFromSettings, the runtime readers), each toward not
	// accusing — the same contract the system settings page saved under.

	next := prev
	next.SetRiskCenterPolicy(pol)
	if err := h.repo.Save(c.Request.Context(), next); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, riskPolicyViewOf(next))
}
