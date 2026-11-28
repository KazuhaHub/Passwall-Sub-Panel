package sharedclient

import (
	"context"
	"sync"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/clientplan"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The heal sweep reconciles every user in turn, and ReconcileOrphans lists the
// whole panel for each of them: U users on P panels were U×P full listings per
// sweep, each as large as the panel has clients, so the cost of a sweep grew
// with the square of the user count. Almost all of those listings end in
// "nothing to do", because a converged user has its desired clients live and no
// stale ones.
//
// A sweep therefore lists each panel ONCE and lets every user ask that listing
// the one question a stale listing can answer safely. The listing is old by
// the time a user's turn comes (a sweep of thousands of users takes minutes,
// and the user's own resync has just provisioned), so:
//
//   - It may say "nothing to clean up": every desired client is live and
//     attached, and the panel holds no other shared-scheme client of this user.
//     Being wrong about that costs a delay: an orphan that appeared after the
//     listing is found by the next sweep.
//   - It may NOT authorise a delete, and it may not say "the replacement is not
//     up yet". For either, ReconcileOrphans reads the panel again and decides
//     on that read alone, exactly as it does outside a sweep. A delete on an
//     old listing could remove the only client serving an inbound the
//     replacement has lost since; "not up yet" on an old listing would be
//     wrong for the client this very resync just created.
//
// So the sweep changes how often the panel is read, never what is deleted.

type orphanSweepKey struct{}

// orphanSweep holds one listing per panel for the lifetime of one sweep.
type orphanSweep struct {
	mu     sync.Mutex
	panels map[int64]*sweptPanel
}

type sweptPanel struct {
	once sync.Once
	live map[string][]int
	err  error
}

// WithOrphanSweep returns a context under which ReconcileOrphans shares one
// listing per panel. The caller owns the lifetime: the context must not outlive
// the sweep, because the listings are never refreshed.
func (s *Service) WithOrphanSweep(ctx context.Context) context.Context {
	return context.WithValue(ctx, orphanSweepKey{}, &orphanSweep{panels: map[int64]*sweptPanel{}})
}

// sweepListing returns the sweep's listing of a panel, reading it on first use.
// Outside a sweep it returns (nil, nil). Concurrent users of one panel wait for
// the same read. A failed read is remembered as failed: every user then falls
// back to its own read, which is what it would have done without a sweep.
func (s *Service) sweepListing(ctx context.Context, panelID int64, cli ports.XUIClient) (map[string][]int, error) {
	sweep, _ := ctx.Value(orphanSweepKey{}).(*orphanSweep)
	if sweep == nil {
		return nil, nil
	}
	sweep.mu.Lock()
	p := sweep.panels[panelID]
	if p == nil {
		p = &sweptPanel{}
		sweep.panels[panelID] = p
	}
	sweep.mu.Unlock()
	p.once.Do(func() { p.live, p.err = cli.ListClientInbounds(ctx) })
	return p.live, p.err
}

// nothingToReconcile reports whether a listing shows a user as converged on a
// panel: every desired client live with at least one inbound, and no other
// client of the shared scheme under this user's id. It reads the listing only.
func nothingToReconcile(live map[string][]int, desired map[string]struct{}, userID int64) bool {
	for email := range desired {
		if len(live[email]) == 0 {
			return false
		}
	}
	for email := range live {
		if _, isDesired := desired[email]; isDesired {
			continue
		}
		if clientplan.IsSharedClientEmail(email, userID) {
			return false
		}
	}
	return true
}
