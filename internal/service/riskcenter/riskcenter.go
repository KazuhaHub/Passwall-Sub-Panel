// Package riskcenter is the read side of the risk center (风控中心): which
// accounts need an admin's look and why (the attention queue, one account's
// drawer, the Users page's levels and the bell's count, all from one
// computation), who is connected from where right now (the live snapshot
// the traffic poll keeps, and an on-demand re-reading of it), where each
// account connected from (connection_history), and every change of
// attention the detectors made (flag_records) — paged, filtered and named
// for the admin view.
//
// IT LOOKS; IT NEVER ACTS. Nothing here decides anything about an account,
// and nothing it is handed can: every dependency is a narrow read interface
// (TestRiskCenterCannotWriteServiceState pins each one's method set), and
// the one call that is not a plain read — a fresh reading of the panels'
// live connections — writes nothing but the in-memory snapshot it is shown
// from and is never a detector sample (traffic.RefreshLiveConnections). No
// verdict and no suspension reads what this package serves.
//
// Admin-only, like the Geo and risk tabs it sits beside: it names accounts
// with their addresses, on signals rather than proof.
package riskcenter

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Deps is everything the risk center reads, each as the narrowest interface
// that serves it. Now is the clock (nil = time.Now).
//
// Geo, Signals, Reviews and Holds are the attention computation's sources.
// A nil one contributes nothing (a test that exercises only the live view
// leaves them out); production wires them all, and
// TestBuildWiresTheRiskCenter drives each through the assembled router.
// Groups names the queue's and the drawer's groups; nil names none.
type Deps struct {
	Live     LiveSource
	Settings SettingsLoader
	Users    UserReader
	Panels   PanelLister
	Fetches  FetchReader
	History  HistoryLister
	Flags    FlagLister
	Geo      GeoReader
	Signals  SignalReader
	Reviews  ReviewReader
	Holds    HoldLister
	Groups   GroupLister
	Now      func() time.Time
}

// LiveSource is the traffic service's live-connection snapshot: the latest
// one stored, and a reading of every panel now (see
// traffic.Service.RefreshLiveConnections for why that is never a detector
// sample).
type LiveSource interface {
	LiveSnapshot() *domain.LiveConnSnapshot
	RefreshLiveConnections(ctx context.Context) (*domain.LiveConnSnapshot, error)
}

// SettingsLoader reads the global settings: the live view's knobs, the
// poll interval its staleness is floored by and the sub-log retention its
// device window is bounded by.
type SettingsLoader interface {
	Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error)
}

// UserReader names accounts: one at a time on a page of the live view and
// in the drawer, and in one read for the queue's page and the admins a
// review names (ListByIDs: the existing accounts among ids, missing ones
// absent).
type UserReader interface {
	GetByID(ctx context.Context, id int64) (*domain.User, error)
	ListByIDs(ctx context.Context, ids []int64) ([]*domain.User, error)
}

// PanelLister names the panels. Only ID and Name are read.
type PanelLister interface {
	List(ctx context.Context) ([]*domain.XUIPanel, error)
}

// FetchReader is the fetch log, read for one page of accounts at a time to
// infer the devices behind their connections.
type FetchReader interface {
	RecentForUsers(ctx context.Context, userIDs []int64, since time.Time, limit int) ([]domain.SubLog, error)
}

// HistoryLister is connection_history's admin read.
type HistoryLister interface {
	List(ctx context.Context, f ports.ConnectionHistoryFilter) ([]domain.ConnectionRecord, int64, error)
}

// FlagLister is flag_records' reads: the admin list, each account's latest
// record (the queue's 最近变化) and the records since a dismissal (the
// reopen rule's steps). Review records are excluded from the last two by
// the store.
type FlagLister interface {
	List(ctx context.Context, f ports.FlagRecordFilter) ([]domain.FlagRecord, int64, error)
	LatestByUsers(ctx context.Context, userIDs []int64) (map[int64]int64, error)
	StepsSince(ctx context.Context, since map[int64]int64) (map[int64][]domain.FlagStep, error)
}

// GeoReader is geo_streaks' reads: the fleet's fresh verdicts at attention
// without their evidence, the count of fresh verdicts nobody could place,
// and the full rows of the accounts on one page (or one drawer).
type GeoReader interface {
	AttentionLevels(ctx context.Context, since time.Time) ([]ports.GeoAttentionRow, error)
	CountFreshUnknown(ctx context.Context, since time.Time) (int64, error)
	ListByUsers(ctx context.Context, userIDs []int64) ([]domain.GeoRecord, error)
}

// SignalReader is risk_signals' reads, on GeoReader's pattern.
type SignalReader interface {
	AttentionLevels(ctx context.Context, since time.Time) ([]ports.SignalAttentionRow, error)
	ListByUsers(ctx context.Context, userIDs []int64) ([]domain.RiskSignal, error)
}

// ReviewReader reads risk_reviews and never writes them: one account's row,
// and every row in force (dismissed or trusted) of an existing account.
// Dismissing and trusting are riskreview's, a separate package, so that
// nothing handed to this one can write a review.
type ReviewReader interface {
	Get(ctx context.Context, userID int64) (domain.RiskReview, bool, error)
	List(ctx context.Context) ([]domain.RiskReview, error)
}

// HoldLister lists the accounts whose service axis carries one reason, with
// the time the hold was written: the detector's own suspensions (geo_auto).
type HoldLister interface {
	ListServiceHolds(ctx context.Context, reason domain.AutoDisabledReason) ([]ports.ServiceHold, error)
}

// GroupLister names the groups. Only ID and Name are read.
type GroupLister interface {
	List(ctx context.Context) ([]*domain.Group, error)
}

// Bounds of the live view and its refresh. Not settings: each is a fact
// about the upstream or a bound on one request, not a policy an operator
// would tune.
const (
	// liveRefreshTimeout bounds one refresh: a panel that has not answered
	// by then is listed as unread, and the request gets its answer. Well
	// under a reverse proxy's usual 60-second read timeout. The SPA waits
	// longer than this for the refresh POST (LIVE_REFRESH_TIMEOUT_MS in
	// web-react/src/api/riskCenter.ts, whose test reads this line): at the
	// shared client's 30 s it would report as failed a refresh that still
	// ran and spent the cooldown. Raising this means raising that too.
	liveRefreshTimeout = 45 * time.Second
	// deviceInferMaxRows bounds the fetch-log rows one page of the live
	// view reads to infer devices (ports.SubLogRecentMaxRows).
	deviceInferMaxRows = ports.SubLogRecentMaxRows
	// inProgressRetry is the Retry-After of a refresh refused because
	// another is still reading the panels: most finish within seconds.
	inProgressRetry = 2 * time.Second
	// liveMaxPageSize caps a page of accounts: one page is one fetch-log
	// read, which refuses more accounts than this (RecentForUsers).
	liveMaxPageSize = ports.SubLogRecentMaxUsers
	// liveDefaultPageSize is the admin lists' default page.
	liveDefaultPageSize = 25
	// defaultPollInterval is the traffic poll's shipped cadence, used when
	// the settings are unreadable or the interval unset.
	defaultPollInterval = 5 * time.Minute
)

// Service serves the risk center's reads. The zero refresh state (nothing
// running, never started) is "a refresh may run now".
type Service struct {
	d   Deps
	now func() time.Time
	// refreshTimeout is liveRefreshTimeout; a field only so a test can
	// shorten it.
	refreshTimeout time.Duration

	// mu guards the refresh's single flight and its cooldown. It is never
	// held across a panel read.
	mu        sync.Mutex
	running   bool
	lastStart time.Time
}

// New builds the service. Every dependency but the attention sources and
// the groups (see Deps) is required by the method that reads it; the
// composition root wires them all (TestBuildWiresTheRiskCenter).
func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{d: d, now: now, refreshTimeout: liveRefreshTimeout}
}

// PanelRef names one panel.
type PanelRef struct {
	ID   int64
	Name string
}

// runtime resolves the knobs every read needs: the fleet-wide risk runtime,
// the traffic poll's interval and the stored settings themselves. An
// unreadable settings table is the shipped defaults and a five-minute poll,
// never a failed view: the view shows what is in memory, and a stale
// warning computed from the defaults is still a warning.
func (s *Service) runtime(ctx context.Context) (domain.RiskRuntime, time.Duration, ports.UISettings) {
	var set ports.UISettings
	if s.d.Settings != nil {
		loaded, err := s.d.Settings.Load(ctx, ports.UISettings{})
		if err != nil {
			log.Warn("risk center: settings unreadable; using the shipped defaults", "err", err)
		} else {
			set = loaded
		}
	}
	poll := defaultPollInterval
	if set.CronTrafficPullMinutes > 0 {
		poll = time.Duration(set.CronTrafficPullMinutes) * time.Minute
	}
	return domain.RiskRuntimeFromSettings(set.RiskRuntimeSettings()), poll, set
}

// panels lists every panel by id, and the same as a name map. Panel names
// are what an admin recognises a connection by; the node under it is a raw
// upstream guid (PSP has no guid-to-name mapping).
func (s *Service) panels(ctx context.Context) ([]PanelRef, map[int64]string, error) {
	list, err := s.d.Panels.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("risk center: list panels: %w", err)
	}
	refs := make([]PanelRef, 0, len(list))
	names := make(map[int64]string, len(list))
	for _, p := range list {
		if p == nil {
			continue
		}
		refs = append(refs, PanelRef{ID: p.ID, Name: p.Name})
		names[p.ID] = p.Name
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	return refs, names, nil
}

// History is one page of connection_history, as the store filters, sorts
// and pages it, plus the names of the panels its rows name (a panel deleted
// since has no name and reads by id). The filter is the store's to
// validate: an unknown exclusion is its ErrValidation.
func (s *Service) History(ctx context.Context, f ports.ConnectionHistoryFilter) ([]domain.ConnectionRecord, map[int64]string, int64, error) {
	rows, total, err := s.d.History.List(ctx, f)
	if err != nil {
		return nil, nil, 0, err
	}
	_, names, err := s.panels(ctx)
	if err != nil {
		return nil, nil, 0, err
	}
	return rows, names, total, nil
}
