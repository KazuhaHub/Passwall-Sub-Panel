package riskcenter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// RefreshResult is what an admin's 立即刷新 did.
type RefreshResult struct {
	// Refreshed is false when no panel was read because a poll had just
	// read them all (Reason "just_polled"); the stored snapshot — that
	// poll's, or a reading that finished after it — is the answer then.
	Refreshed bool
	Reason    string
	// Snapshot is what is stored AFTER the refresh: the refresh's own
	// reading, or a newer poll's that landed while the panels were read —
	// the view always shows the newest reading.
	Snapshot *domain.LiveConnSnapshot
	// Panels is every panel, by id, to name the snapshot's unread and
	// unsupported ones. Empty when the panel listing failed after the
	// refresh itself succeeded: the ids are still in the snapshot.
	Panels []PanelRef
}

// RefreshThrottled is a refresh refused without reading any panel: another
// is still reading them ("in_progress"), or the fleet-wide cooldown has not
// run out ("cooldown"). It is a domain.ErrResourceExhausted, a 429, with the
// time after which a retry would be accepted.
type RefreshThrottled struct {
	Reason     string
	RetryAfter time.Duration
}

func (e *RefreshThrottled) Error() string {
	return fmt.Sprintf("live-connection refresh refused (%s); retry after %s", e.Reason, e.RetryAfter)
}

func (e *RefreshThrottled) Unwrap() error { return domain.ErrResourceExhausted }

// The refresh's outcomes, as psp_live_conn_refresh_total counts them.
const (
	refreshOK         = "ok"
	refreshPartial    = "partial"
	refreshJustPolled = "just_polled"
	refreshCooldown   = "cooldown"
	refreshInProgress = "in_progress"
	refreshError      = "error"
)

// Refresh reads every panel's live connections now and stores them as the
// live view's snapshot (traffic.RefreshLiveConnections: one live read per
// panel, never a detector sample), rationed for the whole fleet:
//
//   - one refresh at a time: another request while one is reading the
//     panels is refused (in_progress), never a second read of every panel;
//   - one per cooldown (risk.live_refresh_cooldown_seconds) for every admin
//     together, counted from the START of the last one — a refresh that
//     failed still asked every panel, and a failing panel is exactly when
//     an admin clicks again and again. The shared-exit rule counts the
//     holders of a source across the fleet, so a refresh is always every
//     panel, and there is no per-account one to ration separately;
//   - right after a poll advanced the per-node references, no panel is read
//     and the stored snapshot is the answer (just_polled): under one
//     upstream rescan later, every node that poll advanced still reads "not
//     rescanned" against them, and the refresh would publish an empty view.
//     The traffic service decides that (domain.ErrLiveJustPolled) in the
//     same critical section in which it copies the references, because
//     nothing visible from here can: the snapshot on display may be a
//     refresh that finished after the poll, and a poll merges its
//     references before it stores its snapshot. No panel was asked, so the
//     cooldown this refresh took is handed back.
//
// The panels are read on the request's values — the backend operation
// gate's admission rides on them, so the read runs inside that admission —
// but not on its cancellation, bounded by liveRefreshTimeout instead; a
// panel still unanswered then is listed as unread. An admin who closes the
// tab mid-read must not cut the reading short: every panel still being
// read would answer "cancelled", and the reading would either list them as
// failed panels or be thrown away, with the fleet-wide cooldown spent
// either way. Run to its bound, the reading that cooldown bought is shown
// to every admin. Outcomes are counted (psp_live_conn_refresh_total) and
// logged by counts only: the snapshot is addresses.
func (s *Service) Refresh(ctx context.Context) (RefreshResult, error) {
	rt, _, _ := s.runtime(ctx)
	now := s.now()

	s.mu.Lock()
	switch {
	case s.running:
		s.mu.Unlock()
		metrics.LiveConnRefreshTotal.With(refreshInProgress).Inc()
		return RefreshResult{}, &RefreshThrottled{Reason: refreshInProgress, RetryAfter: inProgressRetry}
	case !s.lastStart.IsZero() && now.Sub(s.lastStart) < rt.LiveRefreshCooldown:
		left := rt.LiveRefreshCooldown - now.Sub(s.lastStart)
		s.mu.Unlock()
		metrics.LiveConnRefreshTotal.With(refreshCooldown).Inc()
		return RefreshResult{}, &RefreshThrottled{Reason: refreshCooldown, RetryAfter: left}
	}
	prevStart := s.lastStart
	s.running, s.lastStart = true, now
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.refreshTimeout)
	defer cancel()
	snap, err := s.d.Live.RefreshLiveConnections(rctx)
	if errors.Is(err, domain.ErrLiveJustPolled) {
		// No panel was asked. running is still ours, so nobody else has
		// moved lastStart since it was taken.
		s.mu.Lock()
		s.lastStart = prevStart
		s.mu.Unlock()
		metrics.LiveConnRefreshTotal.With(refreshJustPolled).Inc()
		return RefreshResult{Reason: refreshJustPolled, Snapshot: s.d.Live.LiveSnapshot(), Panels: s.panelRefs(ctx)}, nil
	}
	if err != nil {
		metrics.LiveConnRefreshTotal.With(refreshError).Inc()
		log.Warn("risk center: live-connection refresh failed; the view keeps its previous snapshot", "err", err)
		return RefreshResult{}, err
	}
	outcome := refreshOK
	// Unsupported panels (an adapter with no live read, S-UI) are never a
	// partial reading: that is the adapter's permanent shape, and counting
	// it would make partial mean nothing.
	if snap != nil && len(snap.Unread) > 0 {
		outcome = refreshPartial
	}
	metrics.LiveConnRefreshTotal.With(outcome).Inc()
	if snap != nil {
		log.Info("risk center: live connections refreshed", "outcome", outcome, "source", snap.Source,
			"panels", snap.PanelsAsked, "unread", len(snap.Unread), "unsupported", len(snap.Unsupported),
			"connections", len(snap.Conns))
	}
	return RefreshResult{Refreshed: true, Snapshot: snap, Panels: s.panelRefs(ctx)}, nil
}

// panelRefs names the panels for a refresh's answer. The refresh has
// already happened when this runs, so a failed listing costs the names only
// (the snapshot still carries the ids), never the answer. It reads on the
// request's own context: once the admin has gone, the names serve nobody,
// and a listing cut short by that is not a fault worth a Warn.
func (s *Service) panelRefs(ctx context.Context) []PanelRef {
	refs, _, err := s.panels(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("risk center: panels unreadable; the refresh answer carries panel ids only", "err", err)
		}
		return nil
	}
	return refs
}

// refreshAvailableIn is how long until a refresh would be accepted: the
// rest of the cooldown, and at least the in-progress retry while one runs.
func (s *Service) refreshAvailableIn(now time.Time, cooldown time.Duration) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var left time.Duration
	if !s.lastStart.IsZero() {
		left = max(0, cooldown-now.Sub(s.lastStart))
	}
	if s.running {
		left = max(left, inProgressRetry)
	}
	return left
}
