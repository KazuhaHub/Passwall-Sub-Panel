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
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
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

	// Operators may not see admin/operator accounts (mirrors operatorMayView on
	// the single-user endpoints — without this, the Top list leaks those
	// accounts' UPN + usage to an operator). Checked once via the caller's role;
	// the per-user role comes from the already-loaded rows, so no extra queries.
	claims := middleware.ClaimsFrom(c)
	hideStaff := claims != nil && claims.Role == domain.RoleOperator

	// Walk every user in pages, but batch the per-page report fetch
	// through traffic.ReportForUsers — one LatestForUsers +
	// one LastBeforeForUsers per page instead of 2 SELECTs per user.
	rows := []trafficRow{}
	page := 1
	const pageSize = 100
	for {
		users, total, err := h.users.List(c.Request.Context(), ports.UserFilter{
			Pagination: ports.Pagination{Page: page, PageSize: pageSize},
		})
		if err != nil {
			respondError(c, err)
			return
		}
		reports := h.traffic.ReportForUsers(c.Request.Context(), users)
		for _, u := range users {
			if hideStaff && (u.Role == domain.RoleAdmin || u.Role == domain.RoleOperator) {
				continue
			}
			report := reports[u.ID]
			if report == nil {
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

// operatorMayView blocks an operator caller from reading an admin/operator
// account's traffic, mirroring the write-side guard in SetUserUsage. Returns
// false (and writes 403) when access should be denied.
func (h *AdminTrafficHandler) operatorMayView(c *gin.Context, userID int64) bool {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || claims.Role != domain.RoleOperator {
		return true
	}
	target, err := h.users.GetByID(c.Request.Context(), userID)
	if err == nil && (target.Role == domain.RoleAdmin || target.Role == domain.RoleOperator) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Operators cannot view admin or operator accounts"})
		return false
	}
	return true
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
		if !h.operatorMayView(c, userID) {
			return
		}
		h.historyForUser(c, userID, period, since, until)
		return
	}

	// All-scope: ONE GROUP BY query that sums every user's hourly buckets, instead
	// of walking the user list and calling HistoryFor per user (an N+1 — one SELECT
	// per user). users_count comes from a single paged count call.
	_, usersCount, err := h.users.List(c.Request.Context(), ports.UserFilter{
		Pagination: ports.Pagination{Page: 1, PageSize: 1},
	})
	if err != nil {
		respondError(c, err)
		return
	}
	report, err := h.traffic.HistoryForAll(c.Request.Context(), period, since, until)
	if err != nil {
		respondError(c, err)
		return
	}
	items := make([]trafficHistoryItem, len(report.Items))
	for i, item := range report.Items {
		items[i] = trafficHistoryItem{
			Date:       item.Date,
			UpBytes:    item.UpBytes,
			DownBytes:  item.DownBytes,
			TotalBytes: item.TotalBytes,
		}
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
	if !h.operatorMayView(c, id) {
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
			respondError(c, err)
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
	if !h.operatorMayView(c, id) {
		return
	}
	report, err := h.traffic.ReportFor(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user_id":               report.UserID,
		"permanent_total_bytes": report.PermanentTotalBytes,
		"period_used_bytes":     report.PeriodUsedBytes,
		"today_used_bytes":      report.TodayUsedBytes,
	})
}

type userNodeUsageRow struct {
	NodeID      int64  `json:"node_id"`
	DisplayName string `json:"display_name"`
	PanelName   string `json:"panel_name"`
	Region      string `json:"region"`
	ClientEmail string `json:"client_email"`

	LifetimeUpBytes    int64 `json:"lifetime_up_bytes"`
	LifetimeDownBytes  int64 `json:"lifetime_down_bytes"`
	LifetimeTotalBytes int64 `json:"lifetime_total_bytes"`
	PeriodUpBytes      int64 `json:"period_up_bytes"`
	PeriodDownBytes    int64 `json:"period_down_bytes"`
	PeriodTotalBytes   int64 `json:"period_total_bytes"`
	TodayUpBytes       int64 `json:"today_up_bytes"`
	TodayDownBytes     int64 `json:"today_down_bytes"`
	TodayTotalBytes    int64 `json:"today_total_bytes"`
}

// UserNodeUsage returns one user's usage broken down per node (lifetime /
// current-period / today, each split up+down). Read-only; operator-scope
// guarded exactly like UserReport. Rows are sorted by period usage desc so the
// node the user is leaning on this period leads.
func (h *AdminTrafficHandler) UserNodeUsage(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.operatorMayView(c, id) {
		return
	}
	usage, err := h.traffic.UserNodeUsage(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	panelNames := h.loadPanelNames(c.Request.Context())
	rows := make([]userNodeUsageRow, 0, len(usage))
	for _, u := range usage {
		rows = append(rows, userNodeUsageRow{
			NodeID:             u.NodeID,
			DisplayName:        u.DisplayName,
			PanelName:          panelNames[u.PanelID],
			Region:             u.Region,
			ClientEmail:        u.ClientEmail,
			LifetimeUpBytes:    u.LifetimeUpBytes,
			LifetimeDownBytes:  u.LifetimeDownBytes,
			LifetimeTotalBytes: u.LifetimeTotalBytes,
			PeriodUpBytes:      u.PeriodUpBytes,
			PeriodDownBytes:    u.PeriodDownBytes,
			PeriodTotalBytes:   u.PeriodTotalBytes,
			TodayUpBytes:       u.TodayUpBytes,
			TodayDownBytes:     u.TodayDownBytes,
			TodayTotalBytes:    u.TodayTotalBytes,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].PeriodTotalBytes != rows[j].PeriodTotalBytes {
			return rows[i].PeriodTotalBytes > rows[j].PeriodTotalBytes
		}
		return rows[i].LifetimeTotalBytes > rows[j].LifetimeTotalBytes
	})
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

type userServerUsageRow struct {
	PanelID    int64  `json:"panel_id"`
	ServerName string `json:"server_name"`
	NodeCount  int    `json:"node_count"`

	LifetimeUpBytes    int64 `json:"lifetime_up_bytes"`
	LifetimeDownBytes  int64 `json:"lifetime_down_bytes"`
	LifetimeTotalBytes int64 `json:"lifetime_total_bytes"`
	PeriodUpBytes      int64 `json:"period_up_bytes"`
	PeriodDownBytes    int64 `json:"period_down_bytes"`
	PeriodTotalBytes   int64 `json:"period_total_bytes"`
	TodayUpBytes       int64 `json:"today_up_bytes"`
	TodayDownBytes     int64 `json:"today_down_bytes"`
	TodayTotalBytes    int64 `json:"today_total_bytes"`
}

// UserServerUsage returns one user's usage broken down per SERVER (3X-UI panel)
// — the per-node usage aggregated by server. Same read-only / operator-scope
// guard as UserNodeUsage. Sorted by period usage desc so the server the user
// leans on this period leads.
func (h *AdminTrafficHandler) UserServerUsage(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	if !h.operatorMayView(c, id) {
		return
	}
	usage, err := h.traffic.UserServerUsage(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	rows := make([]userServerUsageRow, 0, len(usage))
	for _, u := range usage {
		rows = append(rows, userServerUsageRow{
			PanelID:            u.PanelID,
			ServerName:         u.ServerName,
			NodeCount:          u.NodeCount,
			LifetimeUpBytes:    u.LifetimeUpBytes,
			LifetimeDownBytes:  u.LifetimeDownBytes,
			LifetimeTotalBytes: u.LifetimeTotalBytes,
			PeriodUpBytes:      u.PeriodUpBytes,
			PeriodDownBytes:    u.PeriodDownBytes,
			PeriodTotalBytes:   u.PeriodTotalBytes,
			TodayUpBytes:       u.TodayUpBytes,
			TodayDownBytes:     u.TodayDownBytes,
			TodayTotalBytes:    u.TodayTotalBytes,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].PeriodTotalBytes != rows[j].PeriodTotalBytes {
			return rows[i].PeriodTotalBytes > rows[j].PeriodTotalBytes
		}
		return rows[i].LifetimeTotalBytes > rows[j].LifetimeTotalBytes
	})
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (h *AdminTrafficHandler) Poll(c *gin.Context) {
	if err := h.traffic.PollOnce(c.Request.Context()); err != nil {
		respondError(c, err)
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
	// Operator-scope guard: operators can manage user traffic but
	// can't reach into admin / operator accounts (incl. their own
	// admin's quota — that'd be a privilege-laundering path).
	if claims := middleware.ClaimsFrom(c); claims != nil && claims.Role == domain.RoleOperator {
		target, terr := h.users.GetByID(c.Request.Context(), id)
		if terr == nil && (target.Role == domain.RoleAdmin || target.Role == domain.RoleOperator) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Operators cannot modify admin or operator accounts"})
			return
		}
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
			respondError(c, err)
		}
		return
	}
	report, err := h.traffic.ReportFor(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
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
		respondError(c, err)
		return
	}
	panelNames := h.loadPanelNames(c.Request.Context())
	// Filter out separators first so the batch report fetch only spans
	// real traffic-bearing nodes.
	realNodes := make([]*domain.Node, 0, len(nodes))
	for _, n := range nodes {
		if !n.IsSeparator() {
			realNodes = append(realNodes, n)
		}
	}
	reports := h.traffic.NodeReportForNodes(c.Request.Context(), realNodes)
	rows := make([]nodeTrafficRow, 0, len(realNodes))
	for _, node := range realNodes {
		report := reports[node.ID]
		if report == nil {
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
				respondError(c, err)
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

	// nodes_count from one List (also the only place separators are excluded);
	// the traffic data is one GROUP BY SUM across all nodes instead of a per-node
	// NodeHistoryFor N+1.
	nodes, err := h.nodes.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	nodesCount := 0
	for _, node := range nodes {
		if !node.IsSeparator() {
			nodesCount++
		}
	}
	report, herr := h.traffic.NodesHistoryForAll(c.Request.Context(), period, since, until)
	if herr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": herr.Error()})
		return
	}
	items := make([]trafficHistoryItem, len(report.Items))
	for i, item := range report.Items {
		items[i] = trafficHistoryItem{
			Date:       item.Date,
			UpBytes:    item.UpBytes,
			DownBytes:  item.DownBytes,
			TotalBytes: item.TotalBytes,
		}
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
