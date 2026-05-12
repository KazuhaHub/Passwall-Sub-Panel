package handler

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/traffic"
)

// AdminTrafficHandler exposes /api/admin/traffic — aggregate usage views
// (Top-N this period) and per-user lookups.
type AdminTrafficHandler struct {
	users   ports.UserRepo
	traffic *traffic.Service
}

func NewAdminTrafficHandler(users ports.UserRepo, trafficSvc *traffic.Service) *AdminTrafficHandler {
	return &AdminTrafficHandler{users: users, traffic: trafficSvc}
}

type trafficRow struct {
	UserID              int64  `json:"user_id"`
	Username            string `json:"username"`
	PermanentTotalBytes int64  `json:"permanent_total_bytes"`
	PeriodUsedBytes     int64  `json:"period_used_bytes"`
	TodayUsedBytes      int64  `json:"today_used_bytes"`
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
				Username:            u.Username,
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

// UserReport returns the usage report for one user (admin view).
func (h *AdminTrafficHandler) UserReport(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
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
