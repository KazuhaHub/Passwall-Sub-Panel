package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodehealth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodemetrics"
)

// Admin only, like every other server-detail endpoint. Resource telemetry names
// the host's kernel, its interface names and its distribution — operational
// detail an operator has no reason to read and a subscriber certainly does not.
//
// THE RAW COUNTERS NEVER APPEAR IN ANY RESPONSE. §11.1 is explicit: a uint64 above
// 2^53 loses precision in JavaScript, and a counter the browser rounds disagrees
// with the panel that stored it. Every value below is a rate, a percentage or a
// gauge, and an absent value is null rather than omitted, so the client renders
// one shape instead of branching on which keys exist.

type NodeMetricsService interface {
	Current(ctx context.Context, agentID string) (*nodemetrics.Snapshot, error)
	History(ctx context.Context, agentID string, from, to time.Time, resolution nodemetrics.Resolution) ([]nodemetrics.SeriesPoint, error)
	Interfaces(ctx context.Context, agentID, name string, from, to time.Time) (nodemetrics.InterfaceSeries, error)
	RequestRefresh(agentID string)
}

type AdminNodeMetricsHandler struct {
	panels  ports.XUIPanelRepo
	agents  ports.NodeAgentRepo
	metrics NodeMetricsService
	health  *nodehealth.Source
	now     func() time.Time
}

func NewAdminNodeMetricsHandler(panels ports.XUIPanelRepo, agents ports.NodeAgentRepo, metrics NodeMetricsService, health *nodehealth.Source) (*AdminNodeMetricsHandler, error) {
	if panels == nil || agents == nil || metrics == nil {
		return nil, errors.New("node metrics handler requires panels, agents and metrics")
	}
	return &AdminNodeMetricsHandler{panels: panels, agents: agents, metrics: metrics, health: health, now: time.Now}, nil
}

// resolveAgent maps a server id to its native agent, refusing anything that is not
// PSP-backed.
//
// A 3X-UI PANEL HAS NO AGENT AND CANNOT REPORT TELEMETRY, so the answer is a
// fixed 409 rather than an empty series: an empty chart would say "this server
// reported nothing", where the truth is "this kind of server cannot report".
func (h *AdminNodeMetricsHandler) resolveAgent(c *gin.Context) (string, int64, bool) {
	panelID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return "", 0, false
	}
	panel, err := h.panels.GetByID(c.Request.Context(), panelID)
	if err != nil {
		mapServerError(c, err)
		return "", 0, false
	}
	if domain.NormalizePanelKind(panel.Kind) != domain.PanelKindPSP {
		c.JSON(http.StatusConflict, gin.H{
			"error": "node metrics are only available for PSP-native servers",
			"code":  "node_metrics_unsupported",
		})
		return "", 0, false
	}
	agent, err := h.agents.GetByPanelID(c.Request.Context(), panelID)
	if err != nil {
		mapServerError(c, err)
		return "", 0, false
	}
	return agent.AgentID, panelID, true
}

// Current serves the latest snapshot with its rates.
func (h *AdminNodeMetricsHandler) Current(c *gin.Context) {
	agentID, panelID, ok := h.resolveAgent(c)
	if !ok {
		return
	}
	agent, err := h.agents.GetByAgentID(c.Request.Context(), agentID)
	if err != nil {
		mapServerError(c, err)
		return
	}
	capable := observedCapability(agent)
	snapshot, err := h.metrics.Current(c.Request.Context(), agentID)
	if errors.Is(err, domain.ErrNotFound) {
		snapshot, err = nil, nil
	}
	if err != nil {
		respondError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if snapshot == nil || snapshot.Observation == nil {
		// NO SNAPSHOT IS NOT AN ERROR, and it is not the same answer as an
		// unsupported node: the panel shows "waiting for the first report" for a
		// node that claimed telemetry and "this version does not provide host
		// metrics" for one that never did.
		c.JSON(http.StatusOK, gin.H{
			"available": capable, "freshness": noSampleFreshness(capable), "summary": emptySummary(),
		})
		return
	}
	health := h.healthOf(c.Request.Context(), panelID)
	c.JSON(http.StatusOK, gin.H{
		"available":    true,
		"freshness":    health.Freshness,
		"received_at":  snapshot.Observation.ReceivedAt.UTC(),
		"collected_at": snapshot.Observation.CollectedAt.UTC(),
		"scope": gin.H{
			"deployment":            snapshot.Observation.ResourceScope,
			"resource_scope":        snapshot.Observation.ResourceScope,
			"cgroup_version":        cgroupVersionOf(snapshot.Sample),
			"data_filesystem_scope": filesystemScopeOf(snapshot.Sample),
		},
		"summary": summaryOf(snapshot.Derived),
		"health":  gin.H{"status": health.Status, "findings": findingsOf(health)},
	})
}

func noSampleFreshness(capable bool) string {
	if capable {
		return nodehealth.FreshnessMissing
	}
	return nodehealth.FreshnessUnsupported
}

func (h *AdminNodeMetricsHandler) healthOf(ctx context.Context, panelID int64) nodehealth.Health {
	if h.health == nil {
		return nodehealth.Health{Status: nodehealth.HealthUnavailable, Freshness: nodehealth.FreshnessUnsupported}
	}
	health, err := h.health.Health(ctx, panelID)
	if err != nil && health.Status == "" {
		return nodehealth.Health{Status: nodehealth.HealthUnavailable, Freshness: nodehealth.FreshnessUnsupported}
	}
	return health
}

func findingsOf(health nodehealth.Health) []gin.H {
	findings := make([]gin.H, 0, len(health.Findings))
	for _, finding := range health.Findings {
		findings = append(findings, gin.H{
			"code": finding.Code, "severity": string(finding.Severity),
			"started_at": finding.StartedAt.UTC(), "current_value": finding.CurrentValue,
			"threshold": finding.Threshold, "unit": finding.Unit,
		})
	}
	return findings
}

// window is the parsed and validated range a series request asks for.
type window struct {
	from, to   time.Time
	resolution nodemetrics.Resolution
}

// parseWindow applies §11.2's bounds.
//
// AN UNAVAILABLE RESOLUTION IS A 422 RATHER THAN A SILENT SUBSTITUTION. Quietly
// answering a minute request from the hourly table would give the operator a
// chart that looks like what they asked for and is not, and they would have no
// way to tell.
func (h *AdminNodeMetricsHandler) parseWindow(c *gin.Context) (window, bool) {
	from, fromErr := time.Parse(time.RFC3339, c.Query("from"))
	to, toErr := time.Parse(time.RFC3339, c.Query("to"))
	if fromErr != nil || toErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from and to must be RFC3339 timestamps"})
		return window{}, false
	}
	from, to = from.UTC(), to.UTC()
	if !from.Before(to) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from must be before to"})
		return window{}, false
	}
	if to.Sub(from) > nodemetrics.MaxHistoryRange {
		c.JSON(http.StatusBadRequest, gin.H{"error": "range exceeds the maximum of 90 days"})
		return window{}, false
	}
	requested := c.DefaultQuery("resolution", "auto")
	rawCutoff := h.now().UTC().Add(-nodemetrics.NodeMetricRawRetention)
	wantMinute := to.Sub(from) <= nodemetrics.MaxMinuteRange && !from.Before(rawCutoff)
	resolution := nodemetrics.ResolutionMinute
	switch requested {
	case "minute":
		if !wantMinute {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "minute resolution does not cover this range", "code": "node_metric_resolution_unavailable",
			})
			return window{}, false
		}
	case "hour":
		resolution = nodemetrics.ResolutionHour
	case "auto":
		if !wantMinute {
			resolution = nodemetrics.ResolutionHour
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "resolution must be auto, minute or hour"})
		return window{}, false
	}
	return window{from: from, to: to, resolution: resolution}, true
}

// History serves a series at the requested resolution.
func (h *AdminNodeMetricsHandler) History(c *gin.Context) {
	agentID, _, ok := h.resolveAgent(c)
	if !ok {
		return
	}
	requested, ok := h.parseWindow(c)
	if !ok {
		return
	}
	points, err := h.metrics.History(c.Request.Context(), agentID, requested.from, requested.to, requested.resolution)
	if err != nil {
		respondError(c, err)
		return
	}
	if len(points) > nodemetrics.MaxHistoryPoints {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "the range returns more points than the maximum", "code": "node_metric_resolution_unavailable",
		})
		return
	}
	series := make([]gin.H, 0, len(points))
	for _, point := range points {
		series = append(series, seriesPointOf(point))
	}
	c.Header("Cache-Control", "private, max-age=15")
	c.JSON(http.StatusOK, gin.H{
		"resolution": string(requested.resolution),
		"from":       requested.from, "to": requested.to,
		"series": series,
	})
}

func seriesPointOf(point nodemetrics.SeriesPoint) gin.H {
	entry := gin.H{
		"at": point.At.UTC(), "coverage_seconds": point.CoverageSeconds,
		"cpu_percent": nil, "cpu_scope": nil, "system_cpu_percent": nil,
		"cgroup_cpu_cores_percent": nil, "cgroup_cpu_quota_percent": nil,
		"cgroup_cpu_capacity_percent": nil, "cgroup_cpu_throttled_period_percent": nil,
		"memory_used_percent": nil, "memory_scope": nil, "disk_available_bytes": nil,
		"rx_bps": nil, "tx_bps": nil, "link_utilization_percent": nil,
		"tcp_retrans_percent": nil, "core_cpu_percent": nil, "core_rss_bytes": nil,
	}
	derived := point.Derived
	setFloat(entry, "cpu_percent", derived.CPUPercent)
	setFloat(entry, "system_cpu_percent", derived.SystemCPUPercent)
	setFloat(entry, "cgroup_cpu_cores_percent", derived.CgroupCPUCoresPercent)
	setFloat(entry, "cgroup_cpu_quota_percent", derived.CgroupCPUQuotaPercent)
	setFloat(entry, "cgroup_cpu_capacity_percent", derived.CgroupCPUCapacityPercent)
	setFloat(entry, "cgroup_cpu_throttled_period_percent", derived.CgroupCPUThrottledPeriodPercent)
	setFloat(entry, "memory_used_percent", derived.MemoryUsedPercent)
	setFloat(entry, "disk_used_percent", derived.DiskUsedPercent)
	setFloat(entry, "rx_bps", derived.RXBps)
	setFloat(entry, "tx_bps", derived.TXBps)
	setFloat(entry, "link_utilization_percent", derived.LinkUtilizationPercent)
	setFloat(entry, "tcp_retrans_percent", derived.TCPRetransPercent)
	setFloat(entry, "core_cpu_percent", derived.CoreCPUPercent)
	if derived.CPUScope != nil {
		entry["cpu_scope"] = *derived.CPUScope
	}
	if derived.MemoryScope != nil {
		entry["memory_scope"] = *derived.MemoryScope
	}
	if derived.DiskAvailableBytes != nil {
		entry["disk_available_bytes"] = *derived.DiskAvailableBytes
	}
	if derived.CoreRSSBytes != nil {
		entry["core_rss_bytes"] = *derived.CoreRSSBytes
	}
	return entry
}

// Interfaces serves one interface's series.
//
// The interface parameter is REQUIRED and is matched against the names the agent
// actually reported, so it can never reach a query as free text.
func (h *AdminNodeMetricsHandler) Interfaces(c *gin.Context) {
	agentID, _, ok := h.resolveAgent(c)
	if !ok {
		return
	}
	name := c.Query("interface")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "interface is required"})
		return
	}
	requested, ok := h.parseWindow(c)
	if !ok {
		return
	}
	series, err := h.metrics.Interfaces(c.Request.Context(), agentID, name, requested.from, requested.to)
	if errors.Is(err, domain.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such interface was reported by this node"})
		return
	}
	if err != nil {
		respondError(c, err)
		return
	}
	points := make([]gin.H, 0, len(series.Points))
	for _, point := range series.Points {
		entry := gin.H{
			"at": point.At.UTC(), "coverage_seconds": point.CoverageSeconds,
			"rx_bps": nil, "tx_bps": nil, "link_utilization_percent": nil, "error_ratio": nil,
		}
		setFloat(entry, "rx_bps", point.RXBps)
		setFloat(entry, "tx_bps", point.TXBps)
		setFloat(entry, "link_utilization_percent", point.LinkUtilizationPercent)
		setFloat(entry, "error_ratio", point.ErrorRatio)
		points = append(points, entry)
	}
	c.Header("Cache-Control", "private, max-age=15")
	c.JSON(http.StatusOK, gin.H{
		"resolution": string(requested.resolution), "interface": series.Name,
		"from": requested.from, "to": requested.to, "series": points,
	})
}

// Refresh asks the node for its next sample sooner.
//
// IT CREATES NO DURABLE TASK AND WAITS FOR NOTHING. The window is in memory, it
// has no side effect, and losing it on a restart is allowed — which is why the
// answer is a 202 rather than a body the caller would have to poll.
func (h *AdminNodeMetricsHandler) Refresh(c *gin.Context) {
	agentID, _, ok := h.resolveAgent(c)
	if !ok {
		return
	}
	h.metrics.RequestRefresh(agentID)
	c.Status(http.StatusAccepted)
}

// Health serves the resource-health detail on its own endpoint, so the server
// list never loads a snapshot to render a badge.
func (h *AdminNodeMetricsHandler) Health(c *gin.Context) {
	_, panelID, ok := h.resolveAgent(c)
	if !ok {
		return
	}
	health := h.healthOf(c.Request.Context(), panelID)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"resource_health": health.Status,
		"freshness":       health.Freshness,
		"findings":        findingsOf(health),
	})
}

func emptySummary() gin.H {
	// Every derived field is present and null, so the client renders one shape
	// rather than branching on which keys exist.
	return gin.H{
		"cpu_percent": nil, "cpu_scope": nil, "system_cpu_percent": nil,
		"cgroup_cpu_cores_percent": nil, "cgroup_cpu_quota_percent": nil,
		"cgroup_cpu_capacity_percent": nil, "cgroup_cpu_throttled_period_percent": nil,
		"memory_used_percent": nil, "memory_scope": nil, "disk_used_percent": nil,
		"rx_bps": nil, "tx_bps": nil, "tcp_retrans_percent": nil, "core_rss_bytes": nil,
	}
}

func summaryOf(derived nodemetrics.Derived) gin.H {
	summary := emptySummary()
	if derived.CPUScope != nil {
		summary["cpu_scope"] = *derived.CPUScope
	}
	if derived.MemoryScope != nil {
		summary["memory_scope"] = *derived.MemoryScope
	}
	if derived.CoreRSSBytes != nil {
		summary["core_rss_bytes"] = *derived.CoreRSSBytes
	}
	setFloat(summary, "cpu_percent", derived.CPUPercent)
	setFloat(summary, "system_cpu_percent", derived.SystemCPUPercent)
	setFloat(summary, "cgroup_cpu_cores_percent", derived.CgroupCPUCoresPercent)
	setFloat(summary, "cgroup_cpu_quota_percent", derived.CgroupCPUQuotaPercent)
	setFloat(summary, "cgroup_cpu_capacity_percent", derived.CgroupCPUCapacityPercent)
	setFloat(summary, "cgroup_cpu_throttled_period_percent", derived.CgroupCPUThrottledPeriodPercent)
	setFloat(summary, "memory_used_percent", derived.MemoryUsedPercent)
	setFloat(summary, "disk_used_percent", derived.DiskUsedPercent)
	setFloat(summary, "rx_bps", derived.RXBps)
	setFloat(summary, "tx_bps", derived.TXBps)
	setFloat(summary, "tcp_retrans_percent", derived.TCPRetransPercent)
	return summary
}

func setFloat(target gin.H, key string, value *float64) {
	if value != nil {
		target[key] = *value
	}
}

func observedCapability(agent *domain.NodeAgent) bool {
	for _, capability := range agent.ObservedCapabilities {
		if capability == hostTelemetryCapability {
			return true
		}
	}
	return false
}

// hostTelemetryCapability mirrors the wire constant rather than importing the
// protocol package for one string.
const hostTelemetryCapability = "host.telemetry.v1"

func cgroupVersionOf(sample *domain.NodeHostMetricSample) int {
	if sample == nil {
		return 0
	}
	return sample.CgroupVersion
}

func filesystemScopeOf(sample *domain.NodeHostMetricSample) string {
	if sample == nil {
		return ""
	}
	return sample.DataFilesystemScope
}
