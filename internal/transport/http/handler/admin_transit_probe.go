package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/linkprobe"
)

// probeQualityDTO is a link's or line's probe summary, for the admin: what the
// chip shows. State is ok, degraded, down or unknown ("no data": never probed,
// or the last report is too old).
type probeQualityDTO struct {
	State       string     `json:"state"`
	MedianRTTMS int        `json:"median_rtt_ms"`
	LossPercent int        `json:"loss_percent"`
	Withheld    bool       `json:"withheld"`
	ReportedAt  *time.Time `json:"reported_at,omitempty"`
}

func probeQualityOf(q *domain.ProbeQuality) *probeQualityDTO {
	if q == nil {
		return nil
	}
	dto := &probeQualityDTO{State: string(q.State), MedianRTTMS: q.MedianRTTMS, LossPercent: q.LossPercent, Withheld: q.Withheld}
	if !q.ReportedAt.IsZero() {
		at := q.ReportedAt
		dto.ReportedAt = &at
	}
	return dto
}

// coarseProbeQuality is what a user may see of a line's probes: ok, degraded or
// down (unknown is simply absent) and the median round trip. Never the loss,
// the error class, the withholding or when it was measured.
func coarseProbeQuality(q *domain.ProbeQuality) (quality string, rttMS int) {
	if q == nil {
		return "", 0
	}
	switch q.State {
	case domain.ProbeOK, domain.ProbeDegraded, domain.ProbeDown:
		quality = string(q.State)
	default:
		return "", 0
	}
	if q.State != domain.ProbeDown {
		rttMS = q.MedianRTTMS
	}
	return quality, rttMS
}

// AdminTransitProbeHandler serves the link-probe settings
// (/api/admin/transit/probe-settings) and the stored probe results
// (/api/admin/transit/probes). adminGroup only. It is the one writer of the
// transit_probe_ keys, by the arrangement of the access-control settings: the
// PUT loads the whole record under uiSettingsWriteMu and changes this part.
type AdminTransitProbeHandler struct {
	repo     ports.SettingsRepo
	defaults func() ports.UISettings
	probes   *linkprobe.Service
	state    ports.TransitProbeRepo
	links    ports.TransitLinkRepo
	forwards ports.TransitRepo
	// invalidate refreshes subscriptions after the withheld flags were cleared.
	invalidate func()
}

// NewAdminTransitProbeHandler serves the probe settings and results.
func NewAdminTransitProbeHandler(repo ports.SettingsRepo, probes *linkprobe.Service, state ports.TransitProbeRepo,
	links ports.TransitLinkRepo, forwards ports.TransitRepo, invalidate func()) *AdminTransitProbeHandler {
	return &AdminTransitProbeHandler{repo: repo, defaults: (&AdminSettingsHandler{}).defaults, probes: probes,
		state: state, links: links, forwards: forwards, invalidate: invalidate}
}

type transitProbeSettingsView struct {
	// Settings are the stored values as stored: 0 and "" mean not configured.
	Settings ports.TransitProbeSettings `json:"settings"`
	// Defaults is the shipped value of every key, for placeholders.
	Defaults map[string]any `json:"defaults"`
	// Effective is what the panel runs with.
	Effective ports.TransitProbeEffective `json:"effective"`
}

func transitProbeSettingsViewOf(s ports.UISettings) transitProbeSettingsView {
	return transitProbeSettingsView{
		Settings: s.TransitProbeSettings(), Defaults: ports.TransitProbeSettingsDefaults(),
		Effective: ports.TransitProbeEffectiveOf(s),
	}
}

// GetSettings answers the stored settings with their defaults and effective values.
func (h *AdminTransitProbeHandler) GetSettings(c *gin.Context) {
	s, err := h.repo.Load(c.Request.Context(), h.defaults())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, transitProbeSettingsViewOf(s))
}

// PutSettings merges the sent keys onto the stored settings and saves. A key
// that does not decode (an unknown one included) or a value out of range
// refuses the whole save. Turning probing or withholding off clears every
// withheld flag at once: a setting that is off stops withholding now, not when
// the next report arrives.
func (h *AdminTransitProbeHandler) PutSettings(c *gin.Context) {
	var req destSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if raw := bytes.TrimSpace(req.Settings); len(raw) == 0 || raw[0] != '{' {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Settings must be an object of transit_probe_ keys"})
		return
	}
	uiSettingsWriteMu.Lock()
	defer uiSettingsWriteMu.Unlock()
	prev, err := h.repo.Load(c.Request.Context(), h.defaults())
	if err != nil {
		respondError(c, err)
		return
	}
	next := prev.TransitProbeSettings()
	dec := json.NewDecoder(bytes.NewReader(req.Settings))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&next); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	next.TransitProbeURL = strings.TrimSpace(next.TransitProbeURL)
	if field, err := ports.ValidateTransitProbeSettings(next); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "field": field, "code": "transit_probe_settings_invalid"})
		return
	}
	saved := prev
	saved.SetTransitProbeSettings(next)
	if err := h.repo.Save(c.Request.Context(), saved); err != nil {
		respondError(c, err)
		return
	}
	if !next.TransitProbeEnabled || !next.TransitProbeWithhold {
		cleared, err := h.state.ClearWithheld(c.Request.Context())
		if err != nil {
			respondError(c, err)
			return
		}
		if cleared > 0 && h.invalidate != nil {
			h.invalidate()
		}
	}
	c.JSON(http.StatusOK, transitProbeSettingsViewOf(saved))
}

type transitProbeItemDTO struct {
	PanelID int64  `json:"panel_id"`
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	// What the key names: a link (entry_panel_id, exit_panel_id) or a managed
	// line (node_id, relay_id).
	EntryPanelID int64  `json:"entry_panel_id,omitempty"`
	ExitPanelID  int64  `json:"exit_panel_id,omitempty"`
	NodeID       int64  `json:"node_id,omitempty"`
	RelayID      string `json:"relay_id,omitempty"`

	State         string    `json:"state"`
	Stale         bool      `json:"stale"`
	Attempts      int       `json:"attempts"`
	Failures      int       `json:"failures"`
	LossPercent   int       `json:"loss_percent"`
	RTTMinMS      int       `json:"rtt_min_ms"`
	RTTMedianMS   int       `json:"rtt_median_ms"`
	RTTMaxMS      int       `json:"rtt_max_ms"`
	LastError     string    `json:"last_error,omitempty"`
	LastSuccessMS int64     `json:"last_success_ms,omitempty"`
	Consecutive   int       `json:"consecutive"`
	StateSince    time.Time `json:"state_since"`
	Withheld      bool      `json:"withheld"`
	ReportedAt    time.Time `json:"reported_at"`
	ValidUntil    time.Time `json:"valid_until"`
}

// ListProbes answers the stored results, evaluated now, optionally of one
// server (?panel_id=). Admins see every row, stale ones included (as unknown).
func (h *AdminTransitProbeHandler) ListProbes(c *gin.Context) {
	var panelID int64
	if raw := c.Query("panel_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "panel_id must be a server id"})
			return
		}
		panelID = id
	}
	ctx := c.Request.Context()
	items, err := h.probes.List(ctx, panelID)
	if err != nil {
		respondError(c, err)
		return
	}
	linkEnds, forwards, err := h.resolve(ctx, items)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]transitProbeItemDTO, 0, len(items))
	for _, item := range items {
		dto := transitProbeItemDTO{
			PanelID: item.PanelID, Key: item.ObjectKey, Kind: item.Kind,
			State: string(item.Health), Stale: item.Stale, Attempts: item.Attempts, Failures: item.Failures, LossPercent: item.Loss,
			RTTMinMS: item.RTTMinMS, RTTMedianMS: item.RTTMedianMS, RTTMaxMS: item.RTTMaxMS,
			LastError: item.LastError, LastSuccessMS: item.LastSuccessMS, Consecutive: item.Consecutive,
			StateSince: item.StateSince, Withheld: item.WithheldNow, ReportedAt: item.ReportedAt, ValidUntil: item.ValidUntil,
		}
		if id, ok := keyRowID(item.ObjectKey, "lnk_"); ok {
			if ends, found := linkEnds[id]; found {
				dto.EntryPanelID, dto.ExitPanelID = ends.EntryPanelID, ends.ExitPanelID
			}
		} else if id, ok := keyRowID(item.ObjectKey, "fwd_"); ok {
			if forward, found := forwards[id]; found {
				dto.NodeID, dto.RelayID = forward.NodeID, forward.RelayID
			}
		}
		out = append(out, dto)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].PanelID != out[j].PanelID {
			return out[i].PanelID < out[j].PanelID
		}
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Kind < out[j].Kind
	})
	c.JSON(http.StatusOK, gin.H{"probes": out})
}

func keyRowID(key, prefix string) (int64, bool) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	return id, err == nil && id > 0
}

func (h *AdminTransitProbeHandler) resolve(ctx context.Context, items []linkprobe.Item) (map[int64]domain.TransitLinkEnds, map[int64]domain.TransitForward, error) {
	var linkIDs, forwardIDs []int64
	for _, item := range items {
		if id, ok := keyRowID(item.ObjectKey, "lnk_"); ok {
			linkIDs = append(linkIDs, id)
		} else if id, ok := keyRowID(item.ObjectKey, "fwd_"); ok {
			forwardIDs = append(forwardIDs, id)
		}
	}
	ends := map[int64]domain.TransitLinkEnds{}
	if len(linkIDs) > 0 {
		var err error
		if ends, err = h.links.LinkEnds(ctx, linkIDs); err != nil {
			return nil, nil, err
		}
	}
	byID := map[int64]domain.TransitForward{}
	if len(forwardIDs) > 0 {
		rows, err := h.forwards.ListByIDs(ctx, forwardIDs)
		if err != nil {
			return nil, nil, err
		}
		for _, row := range rows {
			byID[row.ID] = row
		}
	}
	return ends, byID, nil
}
