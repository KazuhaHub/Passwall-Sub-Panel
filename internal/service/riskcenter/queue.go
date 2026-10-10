package riskcenter

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The queue's statuses: which review state it lists.
const (
	// QueueStatusOpen (the default) is 待处理: attention now, and no
	// dismissal covering it.
	QueueStatusOpen = "open"
	// QueueStatusDismissed is 已忽略: a dismissal in force.
	QueueStatusDismissed = "dismissed"
	// QueueStatusTrusted is 已信任: every trusted account, attention or not
	// — trust is a standing exemption, and must be findable to be withdrawn.
	QueueStatusTrusted = "trusted"
	// QueueStatusAll is 全部: every account at attention and every account
	// with a review in force.
	QueueStatusAll = "all"
)

// Bounds of one queue page.
const (
	queueDefaultPageSize = 25
	// queueMaxPageSize caps a page: each page reads the evidence of its
	// accounts, the widest columns the risk center has.
	queueMaxPageSize = 100
)

// QueueQuery filters and pages the queue. Every filter is optional; Status
// "" is QueueStatusOpen. Sources (any listed source present), Level (the
// account's level equals it), AutoSuspended (held by the detector) and
// Urgent (open and flagged or held) narrow the status; Q is a
// case-insensitive substring of the UPN or the display name.
type QueueQuery struct {
	Status         string
	Sources        []string
	Level          domain.FlagLevel
	AutoSuspended  bool
	Urgent         bool
	Q              string
	Page, PageSize int
}

// QueueRow is one account in the queue.
type QueueRow struct {
	// User is the account as read now: its names, group and service state
	// (reason and time) come from here.
	User      *domain.User
	GroupName string
	Attention domain.AccountAttention
	// Geo is the account's geo verdict, non-nil only when geo is among its
	// sources; Signals its risk signals at attention, in RiskKinds order.
	// Both are loaded for the page only.
	Geo     *domain.GeoRecord
	Signals []domain.RiskSignal
	// ChangedAtMS is when the account last changed: its latest detector
	// record, else the time of a geo_auto hold, else 0 (unknown).
	ChangedAtMS int64
}

// QueueCounts are the metric cards: the WHOLE queue, whatever is filtered.
type QueueCounts struct {
	// Online is how many accounts the live snapshot holds; nil before the
	// first snapshot ("never read" is not "nobody online"). OnlineTakenAt is
	// its time (zero with none) and OnlineStale is the live view's Stale.
	Online        *int
	OnlineTakenAt time.Time
	OnlineStale   bool
	// Urgent: open and flagged or held (the bell's number). Flagged and
	// Suspect: open, by the account's level. AutoSuspended: EVERY account
	// the detector holds, dismissed or not — a dismissed account that is
	// still suspended must not vanish from the card named for suspensions.
	// Dismissed: dismissals in force. Trusted: trusted accounts.
	Urgent, Flagged, Suspect, AutoSuspended, Dismissed, Trusted int
	// GeoUnknown is how many fresh geo verdicts could place nobody: a dead
	// or country-only GeoIP database leaves the queue empty, and this says
	// why.
	GeoUnknown int
}

// QueueView is one page of the queue.
type QueueView struct {
	Rows           []QueueRow
	Total          int
	Page, PageSize int
	Counts         QueueCounts
	// GlobalDetectorsOff says the GLOBAL settings turn every detector off
	// (the geo scope and the five risk kinds). A group may still turn one
	// on, which the page's copy says; it is the empty queue's explanation,
	// not a verdict about every account.
	GlobalDetectorsOff bool
}

// normalize validates the query and fills its defaults. An unknown status,
// source or level is refused rather than ignored: an ignored filter answers
// for the whole fleet.
func (q QueueQuery) normalize() (QueueQuery, error) {
	q.Status = strings.TrimSpace(q.Status)
	switch q.Status {
	case "":
		q.Status = QueueStatusOpen
	case QueueStatusOpen, QueueStatusDismissed, QueueStatusTrusted, QueueStatusAll:
	default:
		// Not echoed: it is admin input, and an error may reach a log.
		return q, fmt.Errorf("%w: unknown queue status", domain.ErrValidation)
	}
	var sources []string
	for _, src := range q.Sources {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		// geo_auto is accepted: the SPA filters holds with AutoSuspended,
		// but the source is a real one and refusing it would be arbitrary.
		if !slices.Contains(domain.AttentionSources(), src) {
			return q, fmt.Errorf("%w: unknown attention source", domain.ErrValidation)
		}
		sources = append(sources, src)
	}
	q.Sources = sources
	switch q.Level {
	case domain.FlagLevelNone, domain.FlagLevelSuspect, domain.FlagLevelFlagged:
	default:
		return q, fmt.Errorf("%w: the level filter must be suspect or flagged", domain.ErrValidation)
	}
	q.Q = strings.ToLower(strings.TrimSpace(q.Q))
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.PageSize = queueDefaultPageSize
	}
	q.PageSize = min(q.PageSize, queueMaxPageSize)
	return q, nil
}

// matches applies the status and the attention filters to one account.
func (q QueueQuery) matches(a *domain.AccountAttention) bool {
	switch q.Status {
	case QueueStatusOpen:
		if !a.Open() {
			return false
		}
	case QueueStatusDismissed:
		if !a.DismissedInForce() {
			return false
		}
	case QueueStatusTrusted:
		if !a.Review.Trusted {
			return false
		}
	}
	if len(q.Sources) > 0 && !slices.ContainsFunc(q.Sources, func(src string) bool { return a.Levels[src] != domain.FlagLevelNone }) {
		return false
	}
	if q.Level != domain.FlagLevelNone && a.Levels.Max() != q.Level {
		return false
	}
	if q.AutoSuspended && !a.Levels.Held() {
		return false
	}
	return !q.Urgent || a.Urgent()
}

// Queue is one page of the attention queue: one row per account, worst
// first, with the cards' counts over the whole queue.
//
// It reads, in order: the attention of the fleet (evidence-free, see
// accounts); the filtered accounts' names in one read, dropping any deleted
// since (the queue must not name somebody who no longer exists), then the
// search; their latest detector records in one read; and, for the PAGE
// only, the geo rows and risk signals that explain it, the group names and
// the live snapshot's count. Nothing is read per account.
//
// The order is the urgency class (flagged and held, flagged, held alone,
// suspect — a held account is someone's service waiting on an admin, and
// the bell rings for it), then the latest change, then the id, so a page
// boundary never reorders.
func (s *Service) Queue(ctx context.Context, q QueueQuery) (QueueView, error) {
	q, err := q.normalize()
	if err != nil {
		return QueueView{}, err
	}
	w := s.window(ctx)
	all, err := s.accounts(ctx, w, true)
	if err != nil {
		return QueueView{}, err
	}

	v := QueueView{Page: q.Page, PageSize: q.PageSize, Rows: []QueueRow{}, GlobalDetectorsOff: globalDetectorsOff(w)}
	if v.Counts, err = s.queueCounts(ctx, w, all); err != nil {
		return QueueView{}, err
	}

	var ids []int64
	for uid, a := range all {
		if q.matches(a) {
			ids = append(ids, uid)
		}
	}
	slices.Sort(ids)
	var rows []QueueRow
	if len(ids) > 0 {
		users, err := s.d.Users.ListByIDs(ctx, ids)
		if err != nil {
			return QueueView{}, fmt.Errorf("risk center: name the queue: %w", err)
		}
		for _, u := range users {
			if u == nil || all[u.ID] == nil || !matchesSearch(u, q.Q) {
				continue
			}
			rows = append(rows, QueueRow{User: u, Attention: *all[u.ID]})
		}
	}
	if len(rows) > 0 {
		named := make([]int64, len(rows))
		for i, r := range rows {
			named[i] = r.User.ID
		}
		latest, err := s.d.Flags.LatestByUsers(ctx, named)
		if err != nil {
			return QueueView{}, fmt.Errorf("risk center: latest changes: %w", err)
		}
		for i := range rows {
			r := &rows[i]
			if at, ok := latest[r.User.ID]; ok {
				r.ChangedAtMS = at
			} else if r.Attention.Levels.Held() {
				r.ChangedAtMS = r.Attention.HeldSinceMS
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if ca, cb := a.Attention.Levels.UrgencyClass(), b.Attention.Levels.UrgencyClass(); ca != cb {
			return ca > cb
		}
		if a.ChangedAtMS != b.ChangedAtMS {
			return a.ChangedAtMS > b.ChangedAtMS
		}
		return a.User.ID < b.User.ID
	})
	v.Total = len(rows)

	// Only a page that exists is cut, so an unbounded page number can never
	// overflow the offset (the reason Live gives).
	if last := (len(rows) + q.PageSize - 1) / q.PageSize; q.Page <= last {
		start := (q.Page - 1) * q.PageSize
		v.Rows = rows[start:min(start+q.PageSize, len(rows))]
	}
	if err := s.explain(ctx, v.Rows); err != nil {
		return QueueView{}, err
	}
	return v, nil
}

// matchesSearch is the queue's search: a case-insensitive substring of the
// UPN or the display name (q is already lower-cased and trimmed).
func matchesSearch(u *domain.User, q string) bool {
	return q == "" || strings.Contains(strings.ToLower(u.UPN), q) || strings.Contains(strings.ToLower(u.DisplayName), q)
}

// queueCounts computes the cards over every account, whatever the filters.
func (s *Service) queueCounts(ctx context.Context, w readWindow, all map[int64]*domain.AccountAttention) (QueueCounts, error) {
	var c QueueCounts
	for _, a := range all {
		if a.Urgent() {
			c.Urgent++
		}
		if a.Open() {
			switch a.Levels.Max() {
			case domain.FlagLevelFlagged:
				c.Flagged++
			case domain.FlagLevelSuspect:
				c.Suspect++
			}
		}
		if a.Levels.Held() {
			c.AutoSuspended++
		}
		if a.DismissedInForce() {
			c.Dismissed++
		}
		if a.Review.Trusted {
			c.Trusted++
		}
	}
	if s.d.Geo != nil {
		n, err := s.d.Geo.CountFreshUnknown(ctx, w.geoSince)
		if err != nil {
			return QueueCounts{}, fmt.Errorf("risk center: count unknown geo verdicts: %w", err)
		}
		c.GeoUnknown = int(n)
	}
	c.OnlineStale = true
	if s.d.Live != nil {
		if snap := s.d.Live.LiveSnapshot(); snap != nil {
			n := len(snap.Users)
			c.Online, c.OnlineTakenAt = &n, snap.TakenAt
			// As Live computes Stale, so the card and the 在线 tab never
			// disagree about the same snapshot.
			c.OnlineStale = max(0, w.now.Sub(snap.TakenAt)) > w.rt.SnapshotStaleAfter(w.poll)
		}
	}
	return c, nil
}

// globalDetectorsOff: the global geo scope is off (as the detector reads
// it) and so is every risk kind.
func globalDetectorsOff(w readWindow) bool {
	return domain.GeoPolicyFromSettings(w.set.GeoPolicySettings()).Scope == domain.GeoScopeOff &&
		w.set.RiskSubSpreadOff && w.set.RiskDevicesOff && w.set.RiskUsageShiftOff && w.set.RiskLoginCountryOff && w.set.RiskDestBlockOff
}

// explain loads what the page's rows show beside their levels: each geo
// source's verdict, the risk signals at attention and the group names — one
// read each, for the page's accounts only, and none when the page has none.
func (s *Service) explain(ctx context.Context, rows []QueueRow) error {
	if len(rows) == 0 {
		return nil
	}
	var geoIDs, sigIDs []int64
	for _, r := range rows {
		if r.Attention.Levels[domain.FlagSourceGeo] != domain.FlagLevelNone {
			geoIDs = append(geoIDs, r.User.ID)
		}
		if slices.ContainsFunc(domain.RiskKinds(), func(k domain.RiskKind) bool {
			return r.Attention.Levels[string(k)] != domain.FlagLevelNone
		}) {
			sigIDs = append(sigIDs, r.User.ID)
		}
	}
	geo := map[int64]*domain.GeoRecord{}
	if len(geoIDs) > 0 && s.d.Geo != nil {
		recs, err := s.d.Geo.ListByUsers(ctx, geoIDs)
		if err != nil {
			return fmt.Errorf("risk center: geo evidence: %w", err)
		}
		for i := range recs {
			geo[recs[i].UserID] = &recs[i]
		}
	}
	signals := map[int64][]domain.RiskSignal{}
	if len(sigIDs) > 0 && s.d.Signals != nil {
		list, err := s.d.Signals.ListByUsers(ctx, sigIDs)
		if err != nil {
			return fmt.Errorf("risk center: risk signal evidence: %w", err)
		}
		for _, sig := range list {
			signals[sig.UserID] = append(signals[sig.UserID], sig)
		}
	}
	groups, err := s.groupNames(ctx)
	if err != nil {
		return err
	}
	for i := range rows {
		r := &rows[i]
		r.GroupName = groups[r.User.GroupID]
		if r.Attention.Levels[domain.FlagSourceGeo] != domain.FlagLevelNone {
			r.Geo = geo[r.User.ID]
		}
		r.Signals = attentionSignals(signals[r.User.ID], r.Attention.Levels)
	}
	return nil
}

// attentionSignals keeps the signals of the kinds at attention, in
// RiskKinds order (the store orders kinds alphabetically). Never nil.
func attentionSignals(list []domain.RiskSignal, levels domain.AttentionLevels) []domain.RiskSignal {
	out := make([]domain.RiskSignal, 0, len(list))
	for _, k := range domain.RiskKinds() {
		if levels[string(k)] == domain.FlagLevelNone {
			continue
		}
		for _, sig := range list {
			if sig.Kind == k {
				out = append(out, sig)
				break
			}
		}
	}
	return out
}

// groupNames names every group by id; none without a group store.
func (s *Service) groupNames(ctx context.Context) (map[int64]string, error) {
	out := map[int64]string{}
	if s.d.Groups == nil {
		return out, nil
	}
	groups, err := s.d.Groups.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("risk center: list groups: %w", err)
	}
	for _, g := range groups {
		if g != nil {
			out[g.ID] = g.Name
		}
	}
	return out, nil
}
