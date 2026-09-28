package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/riskcenter"
)

// The risk center's attention reads: the queue (待处理), one account's
// drawer and the Users page's levels. All three are one computation in the
// service (riskcenter.Service.accounts), served here in the shapes the SPA
// reads. Every list is [] and never null, and every optional object is
// null rather than absent: the SPA reads each field without a null check,
// and "nothing" must not look like "not sent".

// attentionEntryDTO is one attention source and its level.
type attentionEntryDTO struct {
	Source string `json:"source"`
	Level  string `json:"level"`
}

// attentionEntries lists the present sources worst first, the hold last
// (AttentionLevels.Sources); [] when there are none.
func attentionEntries(l domain.AttentionLevels) []attentionEntryDTO {
	out := make([]attentionEntryDTO, 0, len(l))
	for _, src := range l.Sources() {
		out = append(out, attentionEntryDTO{Source: src, Level: string(l[src])})
	}
	return out
}

// reviewBadgeDTO is a queue row's review state: whether a dismissal is
// stored, whether it no longer covers the account (reopened, naming the
// sources that escalated) or lapsed, and whether the account is trusted.
type reviewBadgeDTO struct {
	Dismissed bool     `json:"dismissed"`
	Reopened  bool     `json:"reopened"`
	Lapsed    bool     `json:"lapsed"`
	Trusted   bool     `json:"trusted"`
	Escalated []string `json:"escalated"`
}

func reviewBadgeOf(a domain.AccountAttention) reviewBadgeDTO {
	escalated := a.State.Escalated
	if escalated == nil {
		escalated = []string{}
	}
	return reviewBadgeDTO{
		Dismissed: a.Review.Dismissed(), Reopened: a.State.Reopened, Lapsed: a.State.Lapsed,
		Trusted: a.Review.Trusted, Escalated: escalated,
	}
}

// queueItemDTO is one account in the queue. The service state is the
// access decision's (what the Users page shows); the hold's reason and time
// are sent only while the service axis carries one, so the row can say
// who holds the account without a second read.
type queueItemDTO struct {
	UserID                int64                `json:"user_id"`
	UPN                   string               `json:"upn"`
	DisplayName           string               `json:"display_name"`
	GroupID               int64                `json:"group_id"`
	GroupName             string               `json:"group_name"`
	Level                 string               `json:"level"`
	AutoSuspended         bool                 `json:"auto_suspended"`
	Urgent                bool                 `json:"urgent"`
	ServiceState          domain.ServiceStatus `json:"service_state"`
	ServiceDisabledReason string               `json:"service_disabled_reason,omitempty"`
	ServiceDisabledAtMS   int64                `json:"service_disabled_at_ms,omitempty"`
	Sources               []attentionEntryDTO  `json:"sources"`
	// Geo is exactly the /geo-anomalies item, null unless geo is a source.
	Geo *geoAnomalyRow `json:"geo"`
	// Signals are exactly /risk-signals' signals, the attention kinds only.
	Signals     []riskSignalDTO `json:"signals"`
	ChangedAtMS int64           `json:"changed_at_ms"`
	Review      reviewBadgeDTO  `json:"review"`
}

type queueCountsDTO struct {
	// Online and OnlineTakenAt are null before the first snapshot.
	Online        *int       `json:"online"`
	OnlineTakenAt *time.Time `json:"online_taken_at"`
	OnlineStale   bool       `json:"online_stale"`
	Urgent        int        `json:"urgent"`
	Flagged       int        `json:"flagged"`
	Suspect       int        `json:"suspect"`
	AutoSuspended int        `json:"auto_suspended"`
	Dismissed     int        `json:"dismissed"`
	Trusted       int        `json:"trusted"`
	GeoUnknown    int        `json:"geo_unknown"`
}

type queueDTO struct {
	Items              []queueItemDTO `json:"items"`
	Total              int            `json:"total"`
	Page               int            `json:"page"`
	PageSize           int            `json:"page_size"`
	Counts             queueCountsDTO `json:"counts"`
	GlobalDetectorsOff bool           `json:"global_detectors_off"`
}

// serviceHold is the account's service hold as the risk reads send it:
// the reason and its time (unix ms), both zero — and so omitted — while
// the service axis carries none.
func serviceHold(u *domain.User) (string, int64) {
	if u.ServiceDisabledReason == domain.DisabledNone {
		return "", 0
	}
	var at int64
	if u.ServiceDisabledAt != nil {
		at = u.ServiceDisabledAt.UnixMilli()
	}
	return string(u.ServiceDisabledReason), at
}

// queryBool reads an optional boolean from the query string; a malformed
// one is a domain.ErrValidation, for queryID's reason: an ignored filter
// answers for the whole fleet.
func queryBool(c *gin.Context, name string) (bool, error) {
	v := strings.TrimSpace(c.Query(name))
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%w: %s must be true or false", domain.ErrValidation, name)
	}
	return b, nil
}

// Queue serves one page of the attention queue: GET /risk-center/queue with
// status (open, dismissed, trusted, all), source (a comma list), level
// (flagged, suspect), auto_suspended, urgent, q, page and page_size. The
// service validates status, source and level; a malformed switch is refused
// here, before it is asked.
func (h *AdminRiskCenterHandler) Queue(c *gin.Context) {
	if h.unwired(c) {
		return
	}
	p := parsePagination(c)
	q := riskcenter.QueueQuery{
		Status: strings.TrimSpace(c.Query("status")),
		Level:  domain.FlagLevel(strings.TrimSpace(c.Query("level"))),
		Q:      strings.TrimSpace(c.Query("q")),
		Page:   p.Page, PageSize: p.PageSize,
	}
	for _, src := range strings.Split(c.Query("source"), ",") {
		if src = strings.TrimSpace(src); src != "" {
			q.Sources = append(q.Sources, src)
		}
	}
	var err error
	if q.AutoSuspended, err = queryBool(c, "auto_suspended"); err != nil {
		respondError(c, err)
		return
	}
	if q.Urgent, err = queryBool(c, "urgent"); err != nil {
		respondError(c, err)
		return
	}
	v, err := h.svc.Queue(c.Request.Context(), q)
	if err != nil {
		respondError(c, err)
		return
	}

	now := time.Now()
	out := queueDTO{
		Items: make([]queueItemDTO, 0, len(v.Rows)), Total: v.Total, Page: v.Page, PageSize: v.PageSize,
		Counts: queueCountsDTO{
			Online: v.Counts.Online, OnlineStale: v.Counts.OnlineStale,
			Urgent: v.Counts.Urgent, Flagged: v.Counts.Flagged, Suspect: v.Counts.Suspect,
			AutoSuspended: v.Counts.AutoSuspended, Dismissed: v.Counts.Dismissed, Trusted: v.Counts.Trusted,
			GeoUnknown: v.Counts.GeoUnknown,
		},
		GlobalDetectorsOff: v.GlobalDetectorsOff,
	}
	if v.Counts.Online != nil {
		taken := v.Counts.OnlineTakenAt
		out.Counts.OnlineTakenAt = &taken
	}
	for _, r := range v.Rows {
		if r.User == nil {
			continue
		}
		a := r.Attention
		item := queueItemDTO{
			UserID: r.User.ID, UPN: r.User.UPN, DisplayName: r.User.DisplayName,
			GroupID: r.User.GroupID, GroupName: r.GroupName,
			Level: string(a.Levels.Max()), AutoSuspended: a.Levels.Held(), Urgent: a.Urgent(),
			ServiceState: toUserAccessDTO(r.User, now).ServiceState,
			Sources:      attentionEntries(a.Levels),
			Signals:      make([]riskSignalDTO, 0, len(r.Signals)),
			ChangedAtMS:  r.ChangedAtMS,
			Review:       reviewBadgeOf(a),
		}
		item.ServiceDisabledReason, item.ServiceDisabledAtMS = serviceHold(r.User)
		if r.Geo != nil && a.Levels[domain.FlagSourceGeo] != domain.FlagLevelNone {
			row := geoAnomalyRowOf(*r.Geo, r.User)
			item.Geo = &row
		}
		for _, s := range r.Signals {
			if a.Levels[string(s.Kind)] != domain.FlagLevelNone {
				item.Signals = append(item.Signals, riskSignalDTOOf(s))
			}
		}
		out.Items = append(out.Items, item)
	}
	c.JSON(http.StatusOK, out)
}

// riskUserBasicsDTO is the drawer's account: names, role, group, the
// account switch, the quota, the service hold (sent only while one is
// held) and the access decision the Users page shows.
type riskUserBasicsDTO struct {
	ID                    int64         `json:"id"`
	UPN                   string        `json:"upn"`
	DisplayName           string        `json:"display_name"`
	Role                  domain.Role   `json:"role"`
	GroupID               int64         `json:"group_id"`
	GroupName             string        `json:"group_name"`
	Enabled               bool          `json:"enabled"`
	TrafficLimitBytes     int64         `json:"traffic_limit_bytes"`
	ServiceDisabledReason string        `json:"service_disabled_reason,omitempty"`
	ServiceDisableDetail  string        `json:"service_disable_detail,omitempty"`
	ServiceDisabledAtMS   int64         `json:"service_disabled_at_ms,omitempty"`
	Access                userAccessDTO `json:"access"`
}

// riskReviewDTO is the drawer's review: the stored dismissal (with the
// levels it accepted, display only, and the admin's note — admin-only, and
// never copied into a flag record) and trust, the admins named by their
// current UPN ("" when gone; the SPA shows the id), and what the reopen
// rule decides now.
type riskReviewDTO struct {
	Dismissed      bool              `json:"dismissed"`
	DismissedAtMS  int64             `json:"dismissed_at_ms"`
	DismissedBy    int64             `json:"dismissed_by"`
	DismissedByUPN string            `json:"dismissed_by_upn"`
	Note           string            `json:"note"`
	Levels         map[string]string `json:"levels"`
	Reopened       bool              `json:"reopened"`
	Lapsed         bool              `json:"lapsed"`
	Escalated      []string          `json:"escalated"`
	Trusted        bool              `json:"trusted"`
	TrustedAtMS    int64             `json:"trusted_at_ms"`
	TrustedBy      int64             `json:"trusted_by"`
	TrustedByUPN   string            `json:"trusted_by_upn"`
}

// reviewDTOOf is the drawer's review of one account's attention.
func reviewDTOOf(a domain.AccountAttention, byUPN, trustedByUPN string) riskReviewDTO {
	r := a.Review
	levels := map[string]string{}
	for src, l := range r.Accepted.Levels() {
		levels[src] = string(l)
	}
	badge := reviewBadgeOf(a)
	return riskReviewDTO{
		Dismissed: r.Dismissed(), DismissedAtMS: r.DismissedAtMS, DismissedBy: r.DismissedBy, DismissedByUPN: byUPN,
		Note: r.Note, Levels: levels, Reopened: badge.Reopened, Lapsed: badge.Lapsed, Escalated: badge.Escalated,
		Trusted: r.Trusted, TrustedAtMS: r.TrustedAtMS, TrustedBy: r.TrustedBy, TrustedByUPN: trustedByUPN,
	}
}

// riskGeoDTO is the /geo-anomalies item and whether it is stale: nobody
// re-judged it within the queue's window, so it counts toward nothing.
type riskGeoDTO struct {
	geoAnomalyRow
	Stale bool `json:"stale"`
}

// riskSignalStaleDTO is the /risk-signals signal and its staleness.
type riskSignalStaleDTO struct {
	riskSignalDTO
	Stale bool `json:"stale"`
}

// userDeviceDTO is one client behind the account's fetches. Sources are
// addresses (or IPv6 /64s): admin-only, as every risk-center read is.
type userDeviceDTO struct {
	Label       string   `json:"label"`
	DeviceID4   string   `json:"device_id4"`
	ClientType  string   `json:"client_type"`
	UA          string   `json:"ua"`
	Fetches     int      `json:"fetches"`
	FirstAtMS   int64    `json:"first_at_ms"`
	LastAtMS    int64    `json:"last_at_ms"`
	Sources     []string `json:"sources"`
	SourcesMore int      `json:"sources_more"`
}

type riskUserDTO struct {
	User      riskUserBasicsDTO   `json:"user"`
	Attention []attentionEntryDTO `json:"attention"`
	Review    riskReviewDTO       `json:"review"`
	// Geo is null when the detector has no row for the account.
	Geo     *riskGeoDTO          `json:"geo"`
	Signals []riskSignalStaleDTO `json:"signals"`
	// Live is exactly what GET /risk-center/live?user_id=<id>&page=1&page_size=1
	// serves.
	Live               liveDTO         `json:"live"`
	Devices            []userDeviceDTO `json:"devices"`
	DeviceWindowHours  int64           `json:"device_window_hours"`
	DevicesUnavailable bool            `json:"devices_unavailable"`
}

// User serves one account's drawer: GET /risk-center/users/:id. A bad id
// is a 400, a missing account a 404.
func (h *AdminRiskCenterHandler) User(c *gin.Context) {
	if h.unwired(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid id"})
		return
	}
	s, err := h.svc.UserSummary(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	if s.User == nil {
		respondError(c, domain.ErrNotFound)
		return
	}
	u := s.User
	out := riskUserDTO{
		User: riskUserBasicsDTO{
			ID: u.ID, UPN: u.UPN, DisplayName: u.DisplayName, Role: u.Role, GroupID: u.GroupID, GroupName: s.GroupName,
			Enabled: u.Enabled, TrafficLimitBytes: u.TrafficLimitBytes,
			Access: toUserAccessDTO(u, time.Now()),
		},
		Attention:          attentionEntries(s.Attention.Levels),
		Review:             reviewDTOOf(s.Attention, s.DismissedByUPN, s.TrustedByUPN),
		Signals:            make([]riskSignalStaleDTO, 0, len(s.Signals)),
		Live:               liveViewDTO(s.Live, 1, 1),
		Devices:            make([]userDeviceDTO, 0, len(s.Devices)),
		DeviceWindowHours:  int64(s.DeviceWindow / time.Hour),
		DevicesUnavailable: s.DevicesUnavailable,
	}
	out.User.ServiceDisabledReason, out.User.ServiceDisabledAtMS = serviceHold(u)
	if out.User.ServiceDisabledReason != "" {
		out.User.ServiceDisableDetail = u.ServiceDisableDetail
	}
	if s.Geo != nil {
		out.Geo = &riskGeoDTO{geoAnomalyRow: geoAnomalyRowOf(*s.Geo, u), Stale: s.GeoStale}
	}
	for _, sig := range s.Signals {
		out.Signals = append(out.Signals, riskSignalStaleDTO{riskSignalDTO: riskSignalDTOOf(sig), Stale: s.StaleKinds[sig.Kind]})
	}
	for _, d := range s.Devices {
		sources := d.Sources
		if sources == nil {
			sources = []string{}
		}
		out.Devices = append(out.Devices, userDeviceDTO{
			Label: d.Label, DeviceID4: d.DeviceID4, ClientType: d.ClientType, UA: d.UA, Fetches: d.Fetches,
			FirstAtMS: d.FirstAtMS, LastAtMS: d.LastAtMS, Sources: sources, SourcesMore: d.SourcesMore,
		})
	}
	c.JSON(http.StatusOK, out)
}

// levelDTO is one account on the Users page's risk column.
type levelDTO struct {
	Level         string `json:"level"`
	AutoSuspended bool   `json:"auto_suspended"`
	Open          bool   `json:"open"`
	Dismissed     bool   `json:"dismissed"`
	Trusted       bool   `json:"trusted"`
}

// Levels serves the Users page's risk column: GET /risk-center/levels, an
// object keyed by the decimal account id — every account at attention and
// every trusted one (level "" when it has nothing to show). One read for
// the whole list, rather than one per row.
func (h *AdminRiskCenterHandler) Levels(c *gin.Context) {
	if h.unwired(c) {
		return
	}
	levels, err := h.svc.Levels(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	out := make(map[int64]levelDTO, len(levels))
	for id, l := range levels {
		out[id] = levelDTO{Level: string(l.Level), AutoSuspended: l.AutoSuspended, Open: l.Open, Dismissed: l.Dismissed, Trusted: l.Trusted}
	}
	c.JSON(http.StatusOK, out)
}
