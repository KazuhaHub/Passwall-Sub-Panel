package riskcenter

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// UserSummary is everything the drawer shows about one account.
type UserSummary struct {
	User      *domain.User
	GroupName string
	// Attention is UserAttention: exactly what the queue computes for the
	// account, its review included.
	Attention domain.AccountAttention
	// DismissedByUPN and TrustedByUPN name the admins who acted by their
	// CURRENT UPN; "" when that admin no longer exists (the SPA shows the
	// id).
	DismissedByUPN, TrustedByUPN string
	// Geo is the account's geo verdict in ANY state (nil = no row): a clean
	// verdict is the answer to "is this account fine", not a gap. GeoStale
	// says nobody re-judged it within the queue's window, so it is history
	// and counts toward nothing.
	Geo      *domain.GeoRecord
	GeoStale bool
	// Signals are its risk signals of the kinds this build computes, in
	// RiskKinds order, any state; StaleKinds marks each one's staleness on
	// GeoStale's rule.
	Signals    []domain.RiskSignal
	StaleKinds map[domain.RiskKind]bool
	// Live is the live view limited to the account — the same view
	// GET /risk-center/live?user_id= serves.
	Live LiveView
	// Devices are the clients behind the account's fetches over
	// DeviceWindow, newest first. DevicesUnavailable says the fetch log
	// could not be read — which is not "none found".
	Devices            []domain.UserDevice
	DeviceWindow       time.Duration
	DevicesUnavailable bool
}

// UserSummary is one account's drawer. A missing account is
// domain.ErrNotFound. Every read is for this account alone; a fetch-log
// error costs the devices only (they are an inference on top of the rest).
func (s *Service) UserSummary(ctx context.Context, userID int64) (UserSummary, error) {
	w := s.window(ctx)
	u, err := s.account(ctx, userID)
	if err != nil {
		return UserSummary{}, err
	}
	geoRows, sigRows, err := s.verdictsOf(ctx, userID)
	if err != nil {
		return UserSummary{}, err
	}
	// The same rows the attention is computed from, so the verdicts shown
	// and the levels shown cannot come from two different reads.
	att, err := s.attentionOf(ctx, w, u, geoRows, sigRows)
	if err != nil {
		return UserSummary{}, err
	}
	sum := UserSummary{User: u, Attention: att, StaleKinds: map[domain.RiskKind]bool{}}

	for i := range geoRows {
		if geoRows[i].UserID == userID {
			sum.Geo = &geoRows[i]
			sum.GeoStale = geoRows[i].UpdatedAtMS < w.geoSince.UnixMilli()
			break
		}
	}
	for _, k := range domain.RiskKinds() {
		for _, sig := range sigRows {
			if sig.UserID == userID && sig.Kind == k {
				sum.Signals = append(sum.Signals, sig)
				sum.StaleKinds[k] = sig.UpdatedAtMS < w.riskSince.UnixMilli()
				break
			}
		}
	}

	if err := s.nameReviewers(ctx, &sum); err != nil {
		return UserSummary{}, err
	}
	groups, err := s.groupNames(ctx)
	if err != nil {
		return UserSummary{}, err
	}
	sum.GroupName = groups[u.GroupID]

	if sum.Live, err = s.Live(ctx, LiveQuery{UserID: userID, Pagination: ports.Pagination{Page: 1, PageSize: 1}}); err != nil {
		return UserSummary{}, err
	}

	sum.DeviceWindow = deviceWindow(w.rt, w.set)
	since := w.now.Add(-sum.DeviceWindow)
	fetches, err := s.d.Fetches.RecentForUsers(ctx, []int64{userID}, since, deviceInferMaxRows)
	if err != nil {
		// No account id: the log says the fetch log failed, not whom the
		// admin was looking at.
		log.Warn("risk center: fetch log unreadable; the drawer shows no devices", "err", err)
		sum.DevicesUnavailable = true
	} else {
		sum.Devices = domain.UserDevices(fetches, since)
	}
	return sum, nil
}

// nameReviewers resolves the admins a review names, in one read. The row
// keeps ids only; the name is read now, so a renamed admin reads under the
// current name and a deleted one reads as nobody.
func (s *Service) nameReviewers(ctx context.Context, sum *UserSummary) error {
	rev := sum.Attention.Review
	var ids []int64
	if rev.Dismissed() && rev.DismissedBy > 0 {
		ids = append(ids, rev.DismissedBy)
	}
	if rev.Trusted && rev.TrustedBy > 0 && !slices.Contains(ids, rev.TrustedBy) {
		ids = append(ids, rev.TrustedBy)
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	admins, err := s.d.Users.ListByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("risk center: name the reviewers: %w", err)
	}
	for _, a := range admins {
		if a == nil {
			continue
		}
		if rev.Dismissed() && a.ID == rev.DismissedBy {
			sum.DismissedByUPN = a.UPN
		}
		if rev.Trusted && a.ID == rev.TrustedBy {
			sum.TrustedByUPN = a.UPN
		}
	}
	return nil
}
