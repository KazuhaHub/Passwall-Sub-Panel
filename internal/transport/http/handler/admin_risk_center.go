package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/riskcenter"
)

// RiskCenterService is the risk center's read side as the handler uses it
// (riskcenter.Service).
type RiskCenterService interface {
	Live(ctx context.Context, q riskcenter.LiveQuery) (riskcenter.LiveView, error)
	Refresh(ctx context.Context) (riskcenter.RefreshResult, error)
	History(ctx context.Context, f ports.ConnectionHistoryFilter) ([]domain.ConnectionRecord, map[int64]string, int64, error)
	Flags(ctx context.Context, f ports.FlagRecordFilter) ([]domain.FlagRecord, int64, error)
}

// AdminRiskCenterHandler serves the risk center (风控中心): the live
// connections and their on-demand refresh, the connection history and the
// flag records.
//
// adminGroup only, every route. These are accounts named beside their IP
// addresses — the connection history is the one table that keeps them —
// on signals rather than proof, the reasoning that keeps the Geo and risk
// tabs off the operator role. Nothing here acts on an account; the refresh
// reads the panels and changes nothing but the snapshot it is shown from.
// Every refresh, refused or not, is a POST under /api/admin, so it leaves an
// audit row (request and status, no address); the cooldown bounds how many.
type AdminRiskCenterHandler struct{ svc RiskCenterService }

// NewAdminRiskCenterHandler wraps svc. Nil is a deployment that did not wire
// the risk center: every route answers 503.
func NewAdminRiskCenterHandler(svc RiskCenterService) *AdminRiskCenterHandler {
	return &AdminRiskCenterHandler{svc: svc}
}

// unwired answers 503 when the service is absent. Not an empty list: a
// caller cannot tell "nobody is connected" from "this build cannot say" if
// both answer 200 with [].
func (h *AdminRiskCenterHandler) unwired(c *gin.Context) bool {
	if h.svc != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "the risk center is not wired in this deployment"})
	return true
}

type panelRefDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// connRegionDTO is where the geo database put an address: names and a
// region code, never a coordinate (domain.ConnPlace has none to give).
type connRegionDTO struct {
	CountryCode string `json:"country_code"`
	Country     string `json:"country"`
	Region      string `json:"region"`
	RegionCode  string `json:"region_code"`
	City        string `json:"city"`
}

// regionOf is nil for an address nothing placed (an internal one, or no
// database): null says "no place", which an object of empty names would not.
func regionOf(p domain.ConnPlace) *connRegionDTO {
	if p == (domain.ConnPlace{}) {
		return nil
	}
	return &connRegionDTO{CountryCode: p.CountryCode, Country: p.Country, Region: p.Region, RegionCode: p.RegionCode, City: p.City}
}

type connDeviceDTO struct {
	Label      string `json:"label"`
	DeviceID4  string `json:"device_id4"`
	ClientType string `json:"client_type"`
	UA         string `json:"ua"`
	Fetches    int    `json:"fetches"`
	LastAtMS   int64  `json:"last_at_ms"`
}

type liveConnDTO struct {
	PanelID   int64          `json:"panel_id"`
	PanelName string         `json:"panel_name"`
	Node      string         `json:"node"`
	SourceKey string         `json:"source_key"`
	IP        string         `json:"ip"`
	Exclusion string         `json:"exclusion"`
	SeenAt    int64          `json:"seen_at"`
	Region    *connRegionDTO `json:"region"`
	// Devices are INFERRED from the account's fetches from the same source
	// ([] when none matched); the SPA labels them so.
	Devices []connDeviceDTO `json:"devices"`
}

type liveUserDTO struct {
	UserID         int64         `json:"user_id"`
	UPN            string        `json:"upn"`
	DisplayName    string        `json:"display_name"`
	StaleAddresses int           `json:"stale_addresses"`
	UnreadPanels   int           `json:"unread_panels"`
	Connections    []liveConnDTO `json:"connections"`
}

type liveSnapshotDTO struct {
	// TakenAt is null before the first poll.
	TakenAt           *time.Time    `json:"taken_at"`
	Source            string        `json:"source"`
	AgeSeconds        int64         `json:"age_seconds"`
	Stale             bool          `json:"stale"`
	StaleAfterSeconds int64         `json:"stale_after_seconds"`
	PanelsAsked       int           `json:"panels_asked"`
	PanelsUnread      []panelRefDTO `json:"panels_unread"`
	PanelsUnsupported []panelRefDTO `json:"panels_unsupported"`
	UnreferencedNodes int           `json:"unreferenced_nodes"`
	Users             int           `json:"users"`
	Connections       int           `json:"connections"`
	Truncated         int           `json:"truncated"`
}

type liveRefreshStateDTO struct {
	CooldownSeconds    int64 `json:"cooldown_seconds"`
	AvailableInSeconds int64 `json:"available_in_seconds"`
}

type liveDTO struct {
	Snapshot           liveSnapshotDTO     `json:"snapshot"`
	Refresh            liveRefreshStateDTO `json:"refresh"`
	DeviceWindowHours  int64               `json:"device_window_hours"`
	DevicesUnavailable bool                `json:"devices_unavailable"`
	Panels             []panelRefDTO       `json:"panels"`
	Items              []liveUserDTO       `json:"items"`
	Total              int64               `json:"total"`
	Page               int                 `json:"page"`
	PageSize           int                 `json:"page_size"`
}

// Live serves one page of the live view: GET /risk-center/live with page,
// page_size, user_id, panel_id, exclusion, sort_by, sort_dir.
func (h *AdminRiskCenterHandler) Live(c *gin.Context) {
	if h.unwired(c) {
		return
	}
	p := parsePagination(c)
	q := riskcenter.LiveQuery{Pagination: p, Exclusion: strings.TrimSpace(c.Query("exclusion"))}
	uid, err := queryID(c, "user_id")
	if err != nil {
		respondError(c, err)
		return
	}
	pid, err := queryID(c, "panel_id")
	if err != nil {
		respondError(c, err)
		return
	}
	if uid != nil {
		q.UserID = *uid
	}
	if pid != nil {
		q.PanelID = *pid
	}
	v, err := h.svc.Live(c.Request.Context(), q)
	if err != nil {
		respondError(c, err)
		return
	}

	names := make(map[int64]string, len(v.Panels))
	panels := make([]panelRefDTO, 0, len(v.Panels))
	for _, ref := range v.Panels {
		names[ref.ID] = ref.Name
		panels = append(panels, panelRefDTO{ID: ref.ID, Name: ref.Name})
	}
	out := liveDTO{
		Snapshot: liveSnapshotDTO{
			Stale:             v.Stale,
			StaleAfterSeconds: int64(v.StaleAfter / time.Second),
			PanelsUnread:      []panelRefDTO{},
			PanelsUnsupported: []panelRefDTO{},
		},
		Refresh: liveRefreshStateDTO{
			CooldownSeconds:    int64(v.RefreshCooldown / time.Second),
			AvailableInSeconds: ceilSeconds(v.RefreshAvailableIn),
		},
		DeviceWindowHours:  int64(v.DeviceWindow / time.Hour),
		DevicesUnavailable: v.DevicesUnavailable,
		Panels:             panels,
		Items:              make([]liveUserDTO, 0, len(v.Users)),
		Total:              v.Total,
		Page:               q.Page,
		PageSize:           q.PageSize,
	}
	if snap := v.Snapshot; snap != nil {
		taken := snap.TakenAt
		out.Snapshot.TakenAt = &taken
		out.Snapshot.Source = snap.Source
		out.Snapshot.AgeSeconds = int64(v.Age / time.Second)
		out.Snapshot.PanelsAsked = snap.PanelsAsked
		out.Snapshot.PanelsUnread = panelRefs(snap.Unread, names)
		out.Snapshot.PanelsUnsupported = panelRefs(snap.Unsupported, names)
		out.Snapshot.UnreferencedNodes = snap.Unreferenced
		out.Snapshot.Users = len(snap.Users)
		out.Snapshot.Connections = len(snap.Conns)
		out.Snapshot.Truncated = snap.Truncated
	}
	for _, u := range v.Users {
		item := liveUserDTO{
			UserID: u.UserID, UPN: u.UPN, DisplayName: u.DisplayName,
			StaleAddresses: u.Meta.Stale, UnreadPanels: u.Meta.Unread,
			Connections: make([]liveConnDTO, 0, len(u.Conns)),
		}
		for _, conn := range u.Conns {
			dto := liveConnDTO{
				PanelID: conn.PanelID, PanelName: conn.PanelName, Node: conn.Node,
				SourceKey: conn.SourceKey, IP: conn.IP, Exclusion: conn.Exclusion, SeenAt: conn.SeenAt,
				Region:  regionOf(conn.Place),
				Devices: make([]connDeviceDTO, 0, len(conn.Devices)),
			}
			for _, d := range conn.Devices {
				dto.Devices = append(dto.Devices, connDeviceDTO{
					Label: d.Label, DeviceID4: d.DeviceID4, ClientType: d.ClientType, UA: d.UA,
					Fetches: d.Fetches, LastAtMS: d.LastAtMS,
				})
			}
			item.Connections = append(item.Connections, dto)
		}
		out.Items = append(out.Items, item)
	}
	c.JSON(http.StatusOK, out)
}

// Refresh reads every panel's live connections now: POST
// /risk-center/live/refresh. A refused refresh (cooldown, or one already
// running) is a 429 carrying the reason and the seconds to wait, in the body
// and in Retry-After; a refresh answered by the poll that just ran is a 200
// with refreshed false and reason just_polled. The body describes the
// snapshot stored afterwards, which may be a newer poll's.
func (h *AdminRiskCenterHandler) Refresh(c *gin.Context) {
	if h.unwired(c) {
		return
	}
	res, err := h.svc.Refresh(c.Request.Context())
	if err != nil {
		var throttled *riskcenter.RefreshThrottled
		if errors.As(err, &throttled) {
			secs := max(ceilSeconds(throttled.RetryAfter), 1)
			c.Header("Retry-After", strconv.FormatInt(secs, 10))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":               "refresh_throttled",
				"reason":              throttled.Reason,
				"retry_after_seconds": secs,
			})
			return
		}
		respondError(c, err)
		return
	}
	names := make(map[int64]string, len(res.Panels))
	for _, ref := range res.Panels {
		names[ref.ID] = ref.Name
	}
	body := gin.H{
		"refreshed":          res.Refreshed,
		"reason":             res.Reason,
		"taken_at":           nil,
		"source":             "",
		"panels_asked":       0,
		"panels_unread":      []panelRefDTO{},
		"panels_unsupported": []panelRefDTO{},
		"connections":        0,
	}
	if snap := res.Snapshot; snap != nil {
		body["taken_at"] = snap.TakenAt
		body["source"] = snap.Source
		body["panels_asked"] = snap.PanelsAsked
		body["panels_unread"] = panelRefs(snap.Unread, names)
		body["panels_unsupported"] = panelRefs(snap.Unsupported, names)
		body["connections"] = len(snap.Conns)
	}
	c.JSON(http.StatusOK, body)
}

type connHistoryDTO struct {
	UserID      int64          `json:"user_id"`
	UPN         string         `json:"upn"`
	DisplayName string         `json:"display_name"`
	PanelID     int64          `json:"panel_id"`
	PanelName   string         `json:"panel_name"`
	Node        string         `json:"node"`
	SourceKey   string         `json:"source_key"`
	IP          string         `json:"ip"`
	Exclusion   string         `json:"exclusion"`
	Region      *connRegionDTO `json:"region"`
	FirstSeenMS int64          `json:"first_seen_ms"`
	LastSeenMS  int64          `json:"last_seen_ms"`
	Count       int64          `json:"count"`
}

// Connections serves one page of the connection history: GET
// /risk-center/connections with page, page_size, user_id, panel_id,
// exclusion, since, until (RFC3339, on the last sighting), search (or
// keyword), sort_by and sort_dir. The store validates the exclusion and
// allowlists the sort.
func (h *AdminRiskCenterHandler) Connections(c *gin.Context) {
	if h.unwired(c) {
		return
	}
	p := parsePagination(c)
	f := ports.ConnectionHistoryFilter{
		Pagination: p,
		Exclusion:  strings.TrimSpace(c.Query("exclusion")),
		Search:     firstNonEmpty(p.Keyword, c.Query("search")),
	}
	var err error
	if f.UserID, err = queryID(c, "user_id"); err != nil {
		respondError(c, err)
		return
	}
	if f.PanelID, err = queryID(c, "panel_id"); err != nil {
		respondError(c, err)
		return
	}
	if f.Since, err = queryTime(c, "since"); err != nil {
		respondError(c, err)
		return
	}
	if f.Until, err = queryTime(c, "until"); err != nil {
		respondError(c, err)
		return
	}
	rows, names, total, err := h.svc.History(c.Request.Context(), f)
	if err != nil {
		respondError(c, err)
		return
	}
	items := make([]connHistoryDTO, 0, len(rows))
	for _, r := range rows {
		items = append(items, connHistoryDTO{
			UserID: r.UserID, UPN: r.UPN, DisplayName: r.DisplayName,
			PanelID: r.PanelID, PanelName: names[r.PanelID], Node: r.Node,
			SourceKey: r.SourceKey, IP: r.IP, Exclusion: r.Exclusion, Region: regionOf(r.Place),
			FirstSeenMS: r.FirstSeenMS, LastSeenMS: r.LastSeenMS, Count: r.Count,
		})
	}
	c.JSON(http.StatusOK, pagedEnvelope(items, total, p))
}

type flagRecordDTO struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"user_id"`
	UPN         string `json:"upn"`
	DisplayName string `json:"display_name"`
	Source      string `json:"source"`
	Event       string `json:"event"`
	Level       string `json:"level"`
	PrevLevel   string `json:"prev_level"`
	State       string `json:"state"`
	Code        string `json:"code"`
	// Params is the stored JSON verbatim (the store refuses anything that
	// is not valid JSON, which is what makes serving it raw safe); null
	// when the record has none.
	Params json.RawMessage `json:"params"`
	AtMS   int64           `json:"at_ms"`
}

// Flags serves one page of the flag records, newest first: GET
// /risk-center/flags with page, page_size, user_id, source, level, event,
// since and until (RFC3339). The store validates source, level and event.
func (h *AdminRiskCenterHandler) Flags(c *gin.Context) {
	if h.unwired(c) {
		return
	}
	p := parsePagination(c)
	f := ports.FlagRecordFilter{
		Pagination: p,
		Source:     strings.TrimSpace(c.Query("source")),
		Level:      strings.TrimSpace(c.Query("level")),
		Event:      strings.TrimSpace(c.Query("event")),
	}
	var err error
	if f.UserID, err = queryID(c, "user_id"); err != nil {
		respondError(c, err)
		return
	}
	if f.Since, err = queryTime(c, "since"); err != nil {
		respondError(c, err)
		return
	}
	if f.Until, err = queryTime(c, "until"); err != nil {
		respondError(c, err)
		return
	}
	recs, total, err := h.svc.Flags(c.Request.Context(), f)
	if err != nil {
		respondError(c, err)
		return
	}
	items := make([]flagRecordDTO, 0, len(recs))
	for _, r := range recs {
		params := r.Params
		if len(params) == 0 {
			// Nil marshals as null; an empty non-nil value would fail the
			// whole response.
			params = nil
		}
		items = append(items, flagRecordDTO{
			ID: r.ID, UserID: r.UserID, UPN: r.UPN, DisplayName: r.DisplayName,
			Source: r.Source, Event: string(r.Event), Level: string(r.Level), PrevLevel: string(r.PrevLevel),
			State: string(r.State), Code: r.Code, Params: params, AtMS: r.AtMS,
		})
	}
	c.JSON(http.StatusOK, pagedEnvelope(items, total, p))
}

// panelRefs names panel ids from names; a panel deleted since reads by id
// with no name.
func panelRefs(ids []int64, names map[int64]string) []panelRefDTO {
	out := make([]panelRefDTO, 0, len(ids))
	for _, id := range ids {
		out = append(out, panelRefDTO{ID: id, Name: names[id]})
	}
	return out
}

// ceilSeconds rounds up, so "retry after" never says a second too early.
func ceilSeconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64(math.Ceil(d.Seconds()))
}

// queryID reads an optional positive id from the query string. A malformed
// one is a domain.ErrValidation (400), never ignored: on these admin-only
// lists an ignored filter answers for the whole fleet, and a single-account
// lookup would read as everyone's.
func queryID(c *gin.Context, name string) (*int64, error) {
	v := strings.TrimSpace(c.Query(name))
	if v == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("%w: %s must be a positive integer", domain.ErrValidation, name)
	}
	return &id, nil
}

// queryTime reads an optional RFC3339 instant from the query string; a
// malformed one is a domain.ErrValidation, for queryID's reason.
func queryTime(c *gin.Context, name string) (*time.Time, error) {
	v := strings.TrimSpace(c.Query(name))
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, fmt.Errorf("%w: %s must be an RFC 3339 time", domain.ErrValidation, name)
	}
	return &t, nil
}
