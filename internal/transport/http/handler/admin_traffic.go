package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/traffic"
)

// AdminTrafficHandler exposes /api/admin/traffic — aggregate usage views
// (Top-N this period) and per-user / per-node lookups.
type AdminTrafficHandler struct {
	users    ports.UserRepo
	nodes    ports.NodeRepo
	panels   ports.XUIPanelRepo
	traffic  *traffic.Service
	settings ports.SettingsRepo
}

func NewAdminTrafficHandler(users ports.UserRepo, nodes ports.NodeRepo, panels ports.XUIPanelRepo, trafficSvc *traffic.Service, settings ports.SettingsRepo) *AdminTrafficHandler {
	return &AdminTrafficHandler{users: users, nodes: nodes, panels: panels, traffic: trafficSvc, settings: settings}
}

// loadPanelNames mirrors AdminNodeHandler.loadPanelNames — fetches all panels
// and returns a panel_id → name map. Used to populate the panel_name DTO
// field after the v3 schema dropped the redundant column from nodes.
func (h *AdminTrafficHandler) loadPanelNames(ctx context.Context) map[int64]string {
	names := make(map[int64]string)
	if h.panels == nil {
		return names
	}
	panels, err := h.panels.List(ctx)
	if err != nil {
		return names
	}
	for _, p := range panels {
		names[p.ID] = p.Name
	}
	return names
}

type trafficRow struct {
	UserID              int64  `json:"user_id"`
	UPN                 string `json:"upn"`
	PermanentTotalBytes int64  `json:"permanent_total_bytes"`
	PeriodUsedBytes     int64  `json:"period_used_bytes"`
	TodayUsedBytes      int64  `json:"today_used_bytes"`
}

type setUserTrafficRequest struct {
	PeriodUsedGB float64 `json:"period_used_gb"`
}

type trafficHistoryItem struct {
	Date       string `json:"date"`
	UpBytes    int64  `json:"up_bytes"`
	DownBytes  int64  `json:"down_bytes"`
	TotalBytes int64  `json:"total_bytes"`
}

// Top returns the top-N users by current period usage. N defaults to 20.
func (h *AdminTrafficHandler) Top(c *gin.Context) {
	n, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if n <= 0 {
		n = 20
	}

	// Walk every user, build per-user report. This is O(users * traffic
	// queries). Acceptable at friend-circle scale; revisit if it grows.
	rows := []trafficRow{}
	page := 1
	const pageSize = 100
	for {
		users, total, err := h.users.List(c.Request.Context(), ports.UserFilter{
			Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, u := range users {
			report, err := h.traffic.ReportFor(c.Request.Context(), u.ID)
			if err != nil || report == nil {
				continue
			}
			rows = append(rows, trafficRow{
				UserID:              u.ID,
				UPN:                 u.UPN,
				PermanentTotalBytes: report.PermanentTotalBytes,
				PeriodUsedBytes:     report.PeriodUsedBytes,
				TodayUsedBytes:      report.TodayUsedBytes,
			})
		}
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].PeriodUsedBytes > rows[j].PeriodUsedBytes
	})
	if len(rows) > n {
		rows = rows[:n]
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (h *AdminTrafficHandler) History(c *gin.Context) {
	period, since, until, err := parseTrafficHistoryQuery(c, paneltz.Location(c.Request.Context(), h.settings))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if rawUserID := c.Query("user_id"); rawUserID != "" {
		userID, err := strconv.ParseInt(rawUserID, 10, 64)
		if err != nil || userID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user_id"})
			return
		}
		h.historyForUser(c, userID, period, since, until)
		return
	}

	items := []trafficHistoryItem{}
	usersCount := 0
	page := 1
	const pageSize = 100
	for {
		users, total, err := h.users.List(c.Request.Context(), ports.UserFilter{
			Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, u := range users {
			report, err := h.traffic.HistoryFor(c.Request.Context(), u.ID, period, since, until)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			if len(items) == 0 {
				items = make([]trafficHistoryItem, len(report.Items))
				for i, item := range report.Items {
					items[i].Date = item.Date
				}
			}
			for i, item := range report.Items {
				if i >= len(items) {
					break
				}
				items[i].UpBytes += item.UpBytes
				items[i].DownBytes += item.DownBytes
				items[i].TotalBytes += item.TotalBytes
			}
			usersCount++
		}
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}
	c.JSON(http.StatusOK, gin.H{
		"scope":       "all",
		"period":      period,
		"since":       since.Format("2006-01-02"),
		"until":       until.Format("2006-01-02"),
		"users_count": usersCount,
		"items":       items,
	})
}

func (h *AdminTrafficHandler) UserHistory(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	period, since, until, err := parseTrafficHistoryQuery(c, paneltz.Location(c.Request.Context(), h.settings))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.historyForUser(c, id, period, since, until)
}

func (h *AdminTrafficHandler) historyForUser(c *gin.Context, userID int64, period traffic.HistoryPeriod, since, until time.Time) {
	report, err := h.traffic.HistoryFor(c.Request.Context(), userID, period, since, until)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrValidation):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"scope":   "user",
		"user_id": report.UserID,
		"period":  report.Period,
		"since":   report.Since,
		"until":   report.Until,
		"items":   historyItems(report.Items),
	})
}

// UserReport returns the usage report for one user (admin view).
func (h *AdminTrafficHandler) UserReport(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	report, err := h.traffic.ReportFor(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user_id":               report.UserID,
		"permanent_total_bytes": report.PermanentTotalBytes,
		"period_used_bytes":     report.PeriodUsedBytes,
		"today_used_bytes":      report.TodayUsedBytes,
	})
}

func (h *AdminTrafficHandler) Poll(c *gin.Context) {
	if err := h.traffic.PollOnce(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *AdminTrafficHandler) SetUserUsage(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	var req setUserTrafficRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.PeriodUsedGB < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Period_used_gb must be >= 0"})
		return
	}
	usedBytes := int64(req.PeriodUsedGB * 1024 * 1024 * 1024)
	if err := h.traffic.SetPeriodUsage(c.Request.Context(), id, usedBytes); err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		case errors.Is(err, domain.ErrValidation):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	report, err := h.traffic.ReportFor(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user_id":               report.UserID,
		"permanent_total_bytes": report.PermanentTotalBytes,
		"period_used_bytes":     report.PeriodUsedBytes,
		"today_used_bytes":      report.TodayUsedBytes,
	})
}

func parseTrafficHistoryQuery(c *gin.Context, defaultLoc *time.Location) (traffic.HistoryPeriod, time.Time, time.Time, error) {
	// tz lets the client express which timezone since/until are in. Without
	// it we fall back to the caller-supplied defaultLoc (panel timezone for
	// admin handlers, user-side handlers too — both share this helper).
	// Without any of this a browser in PT asking for "until 2026-05-16"
	// against a UTC server would parse it as 2026-05-16 00:00 UTC, dropping
	// post-midnight-UTC snapshots from the same wall-clock day.
	loc := defaultLoc
	if loc == nil {
		loc = time.Local
	}
	if tz := strings.TrimSpace(c.Query("tz")); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return "", time.Time{}, time.Time{}, fmt.Errorf("invalid tz %q", tz)
		}
		loc = l
	}
	now := time.Now().In(loc)
	defaultUntil := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	defaultSince := defaultUntil.AddDate(0, 0, -29)
	period := traffic.HistoryPeriod(c.DefaultQuery("period", string(traffic.HistoryDay)))
	switch period {
	case traffic.HistoryHour, traffic.HistoryDay, traffic.HistoryWeek, traffic.HistoryMonth:
	default:
		return "", time.Time{}, time.Time{}, errors.New("period must be hour, day, week, or month")
	}

	since, err := parseDateQuery(c.Query("since"), defaultSince, loc)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	until, err := parseDateQuery(c.Query("until"), defaultUntil, loc)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	if until.Before(since) {
		return "", time.Time{}, time.Time{}, errors.New("until must be on or after since")
	}
	if until.Sub(since) > 366*24*time.Hour {
		return "", time.Time{}, time.Time{}, errors.New("date range must be 366 days or less")
	}
	return period, since, until, nil
}

func parseDateQuery(raw string, fallback time.Time, loc *time.Location) (time.Time, error) {
	if raw == "" {
		return fallback, nil
	}
	t, err := time.ParseInLocation("2006-01-02", raw, loc)
	if err != nil {
		return time.Time{}, errors.New("date must use YYYY-MM-DD")
	}
	return t, nil
}

func historyItems(items []traffic.HistoryItem) []trafficHistoryItem {
	out := make([]trafficHistoryItem, len(items))
	for i, item := range items {
		out[i] = trafficHistoryItem{
			Date:       item.Date,
			UpBytes:    item.UpBytes,
			DownBytes:  item.DownBytes,
			TotalBytes: item.TotalBytes,
		}
	}
	return out
}

type nodeTrafficRow struct {
	NodeID              int64    `json:"node_id"`
	DisplayName         string   `json:"display_name"`
	PanelName           string   `json:"panel_name"`
	Region              string   `json:"region"`
	Tags                []string `json:"tags"`
	PermanentTotalBytes int64    `json:"permanent_total_bytes"`
	PeriodUsedBytes     int64    `json:"period_used_bytes"`
	TodayUsedBytes      int64    `json:"today_used_bytes"`
}

// NodesTop returns the top-N nodes by current-month usage. N defaults to 20.
func (h *AdminTrafficHandler) NodesTop(c *gin.Context) {
	n, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if n <= 0 {
		n = 20
	}
	nodes, err := h.nodes.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	panelNames := h.loadPanelNames(c.Request.Context())
	rows := make([]nodeTrafficRow, 0, len(nodes))
	for _, node := range nodes {
		if node.IsSeparator() {
			continue // layout-only rows have no traffic
		}
		report, rerr := h.traffic.NodeReportFor(c.Request.Context(), node.ID)
		if rerr != nil || report == nil {
			continue
		}
		rows = append(rows, nodeTrafficRow{
			NodeID:              node.ID,
			DisplayName:         node.DisplayName,
			PanelName:           panelNames[node.PanelID],
			Region:              node.Region,
			Tags:                node.Tags,
			PermanentTotalBytes: report.PermanentTotalBytes,
			PeriodUsedBytes:     report.PeriodUsedBytes,
			TodayUsedBytes:      report.TodayUsedBytes,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].PeriodUsedBytes > rows[j].PeriodUsedBytes
	})
	if len(rows) > n {
		rows = rows[:n]
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

// NodesHistory returns aggregate per-bucket history across all nodes (or a
// single node when ?node_id= is passed).
func (h *AdminTrafficHandler) NodesHistory(c *gin.Context) {
	period, since, until, err := parseTrafficHistoryQuery(c, paneltz.Location(c.Request.Context(), h.settings))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if rawID := c.Query("node_id"); rawID != "" {
		nodeID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || nodeID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid node_id"})
			return
		}
		report, err := h.traffic.NodeHistoryFor(c.Request.Context(), nodeID, period, since, until)
		if err != nil {
			switch {
			case errors.Is(err, domain.ErrValidation):
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"scope":   "node",
			"node_id": report.NodeID,
			"period":  report.Period,
			"since":   report.Since,
			"until":   report.Until,
			"items":   historyItems(report.Items),
		})
		return
	}

	nodes, err := h.nodes.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := []trafficHistoryItem{}
	nodesCount := 0
	for _, node := range nodes {
		if node.IsSeparator() {
			continue // layout-only rows have no traffic
		}
		report, herr := h.traffic.NodeHistoryFor(c.Request.Context(), node.ID, period, since, until)
		if herr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": herr.Error()})
			return
		}
		if len(items) == 0 {
			items = make([]trafficHistoryItem, len(report.Items))
			for i, item := range report.Items {
				items[i].Date = item.Date
			}
		}
		for i, item := range report.Items {
			if i >= len(items) {
				break
			}
			items[i].UpBytes += item.UpBytes
			items[i].DownBytes += item.DownBytes
			items[i].TotalBytes += item.TotalBytes
		}
		nodesCount++
	}
	c.JSON(http.StatusOK, gin.H{
		"scope":       "all",
		"period":      period,
		"since":       since.Format("2006-01-02"),
		"until":       until.Format("2006-01-02"),
		"nodes_count": nodesCount,
		"items":       items,
	})
}
