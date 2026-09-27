package riskcenter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// LiveQuery narrows and pages the live view. Every filter is optional (0 and
// "" mean any) and narrows the CONNECTIONS; an account left with none is not
// listed. Pagination pages ACCOUNTS: SortBy "connections" (the default; most
// first unless SortDir is "asc") or "user_id" (ascending unless SortDir is
// "desc"); an unknown sort is the default, as in every admin list. Keyword
// is not read.
type LiveQuery struct {
	ports.Pagination
	UserID, PanelID int64
	// Exclusion: "", ports.ConnExclusionKept, ports.ConnExclusionExcluded,
	// or one reason (domain.AddressExcludedInternal, Listed, Infra,
	// Shared). Anything else is a domain.ErrValidation.
	Exclusion string
}

// LiveConn is one connection as the view shows it: the snapshot's, its
// panel's name, and the devices inferred behind it (newest first; none when
// nothing matched or the source is not the account's own egress).
type LiveConn struct {
	domain.LiveConnection
	PanelName string
	Devices   []domain.ConnDevice
}

// LiveUser is one account on the page and its connections, in the
// snapshot's order (panel, node, source).
type LiveUser struct {
	UserID           int64
	UPN, DisplayName string
	// Meta is what the list does not show: the addresses the upstream still
	// remembers but nobody restamped, and the account's unread panels.
	Meta  domain.LiveUserMeta
	Conns []LiveConn
}

// LiveView is one page of the live view and what the page needs to say
// about the snapshot it came from.
type LiveView struct {
	// Snapshot is the stored snapshot the page was cut from; nil before the
	// first poll. Shared and immutable: never modify it.
	Snapshot *domain.LiveConnSnapshot
	// Age is how old the snapshot is on the service's clock (0 with none).
	// Stale says it is older than StaleAfter (always true with no
	// snapshot): the poll has stopped refreshing it, and it is a picture of
	// then, not now.
	Age        time.Duration
	Stale      bool
	StaleAfter time.Duration
	// RefreshCooldown is the configured cooldown; RefreshAvailableIn how
	// long until a refresh would be accepted (0 = now).
	RefreshCooldown    time.Duration
	RefreshAvailableIn time.Duration
	// DeviceWindow is how far back devices were inferred from;
	// DevicesUnavailable says the fetch log could not be read, so no
	// connection has devices — which is not "none found".
	DeviceWindow       time.Duration
	DevicesUnavailable bool
	// Panels is every panel, by id: the filter's choices and the names.
	Panels []PanelRef
	// Users is the page; Total the accounts the filter matched. Total can
	// count an account deleted since the snapshot, which the page drops,
	// until the next reading.
	Users []LiveUser
	Total int64
}

// Live is one page of the live view, cut from the stored snapshot.
//
// It reads the snapshot in memory, the settings, one panel listing, the
// PAGE's accounts (never the fleet's: thousands of connected accounts are
// one snapshot, not thousands of user reads per click) and one page of the
// fetch log to infer their devices. It reads no panel: the snapshot is what
// the last poll or refresh read, and the view says when (Snapshot.TakenAt)
// and whether that is too long ago (Stale).
//
// An account deleted since the snapshot is dropped from the page: the view
// must not name somebody who no longer exists. A users-table error is an
// error, never "deleted". A fetch-log error costs the devices only
// (DevicesUnavailable): they are an inference on top of the connections,
// and the connections are the point.
func (s *Service) Live(ctx context.Context, q LiveQuery) (LiveView, error) {
	switch q.Exclusion {
	case "", ports.ConnExclusionKept, ports.ConnExclusionExcluded,
		domain.AddressExcludedInternal, domain.AddressExcludedListed, domain.AddressExcludedInfra, domain.AddressExcludedShared:
	default:
		// Not echoed: it is admin input to a view of addresses, and an
		// error may reach a log.
		return LiveView{}, fmt.Errorf("%w: unknown live-connection exclusion filter", domain.ErrValidation)
	}
	rt, poll, set := s.runtime(ctx)
	now := s.now()
	snap := s.d.Live.LiveSnapshot()

	v := LiveView{
		Snapshot:        snap,
		StaleAfter:      rt.SnapshotStaleAfter(poll),
		RefreshCooldown: rt.LiveRefreshCooldown,
		DeviceWindow:    deviceWindow(rt, set),
		Users:           []LiveUser{},
	}
	if snap != nil {
		v.Age = max(0, now.Sub(snap.TakenAt))
	}
	v.Stale = snap == nil || v.Age > v.StaleAfter
	v.RefreshAvailableIn = s.refreshAvailableIn(now, rt.LiveRefreshCooldown)

	refs, names, err := s.panels(ctx)
	if err != nil {
		return LiveView{}, err
	}
	v.Panels = refs
	if snap == nil {
		return v, nil
	}

	// Filter, then group by account in the snapshot's order (it is sorted
	// by account, panel, node, source), so each account's connections stay
	// in that order.
	type group struct {
		uid   int64
		conns []domain.LiveConnection
	}
	var groups []*group
	byUser := map[int64]*group{}
	for _, c := range snap.Conns {
		if (q.UserID > 0 && c.UserID != q.UserID) || (q.PanelID > 0 && c.PanelID != q.PanelID) || !exclusionMatches(q.Exclusion, c.Exclusion) {
			continue
		}
		g := byUser[c.UserID]
		if g == nil {
			g = &group{uid: c.UserID}
			byUser[c.UserID] = g
			groups = append(groups, g)
		}
		g.conns = append(g.conns, c)
	}
	sortBy := strings.TrimSpace(q.SortBy)
	desc := !strings.EqualFold(q.SortDir, "asc")
	if sortBy == "user_id" {
		desc = strings.EqualFold(q.SortDir, "desc")
	}
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if sortBy != "user_id" && len(a.conns) != len(b.conns) {
			if desc {
				return len(a.conns) > len(b.conns)
			}
			return len(a.conns) < len(b.conns)
		}
		// Ties (and the user_id sort) by id, so a page boundary never
		// reorders accounts with the same count.
		if sortBy == "user_id" && desc {
			return a.uid > b.uid
		}
		return a.uid < b.uid
	})
	v.Total = int64(len(groups))

	pageNo, size := q.Page, q.PageSize
	if pageNo < 1 {
		pageNo = 1
	}
	if size <= 0 {
		size = liveDefaultPageSize
	}
	size = min(size, liveMaxPageSize)
	start := min((pageNo-1)*size, len(groups))
	pageGroups := groups[start:min(start+size, len(groups))]

	users := make([]LiveUser, 0, len(pageGroups))
	var pageConns []domain.LiveConnection
	for _, g := range pageGroups {
		u, err := s.d.Users.GetByID(ctx, g.uid)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && u == nil) {
			continue
		}
		if err != nil {
			return LiveView{}, fmt.Errorf("risk center: read account %d: %w", g.uid, err)
		}
		lu := LiveUser{UserID: g.uid, UPN: u.UPN, DisplayName: u.DisplayName, Meta: snap.Users[g.uid]}
		for _, c := range g.conns {
			lu.Conns = append(lu.Conns, LiveConn{LiveConnection: c, PanelName: names[c.PanelID]})
		}
		users = append(users, lu)
		pageConns = append(pageConns, g.conns...)
	}

	if len(users) > 0 {
		ids := make([]int64, len(users))
		for i, u := range users {
			ids[i] = u.UserID
		}
		since := now.Add(-v.DeviceWindow)
		fetches, err := s.d.Fetches.RecentForUsers(ctx, ids, since, deviceInferMaxRows)
		if err != nil {
			// Counts only: the page's accounts and their addresses stay out
			// of the log.
			log.Warn("risk center: fetch log unreadable; the live view shows no inferred devices",
				"accounts", len(ids), "err", err)
			v.DevicesUnavailable = true
		} else {
			devices := domain.InferConnectionDevices(pageConns, fetches, since)
			for i := range users {
				for j := range users[i].Conns {
					c := &users[i].Conns[j]
					c.Devices = devices[domain.UserSource{UserID: c.UserID, SourceKey: c.SourceKey}]
				}
			}
		}
	}
	v.Users = users
	return v, nil
}

// exclusionMatches applies the live view's exclusion filter to one source's
// exclusion ("" when the detector judged it). The filter was validated.
func exclusionMatches(filter, exclusion string) bool {
	switch filter {
	case "":
		return true
	case ports.ConnExclusionKept:
		return exclusion == ""
	case ports.ConnExclusionExcluded:
		return exclusion != ""
	default:
		return exclusion == filter
	}
}

// deviceWindow is how far back the view infers devices: the configured
// window, shortened to the sub-log retention when that is set and shorter.
// A window past the retention would read rows that were pruned and present
// hours of fetches as the configured day. The retention is applied here,
// where the log is read, never in the runtime (which carries what was
// configured, as the risk worker's fetch window does).
func deviceWindow(rt domain.RiskRuntime, set ports.UISettings) time.Duration {
	w := rt.DeviceInferWindow
	if days := set.SubLogRetentionDays; days > 0 {
		w = min(w, time.Duration(days)*24*time.Hour)
	}
	return w
}
