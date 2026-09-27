// Package risk computes the observe-only risk signals on the worker's
// cadence (risk.refresh_interval_minutes, hourly by default) and stores one
// verdict per account per signal in risk_signals.
//
// Observation only, by construction: every dependency is a read-only
// interface except the store, whose only writer is its own table. Nothing
// here suspends, notifies or otherwise touches an account — a verdict is
// something an admin reads, and TestRiskServiceCannotWriteServiceState holds
// the package to that.
//
// Each signal is judged independently (one of v2's seven states plus a
// code), with no score and no escalation from one signal to another. A
// signal whose source could not be read this run is skipped, and its stored
// rows stay as they were: an older verdict is better than one judged from
// missing data.
package risk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Deps is everything the worker reads, and the one store it writes.
//
// Every dependency is read-only except Store, which writes only
// risk_signals — and, inside the same transaction, the flag_records of the
// attention changes a save makes, which the worker never sees. No
// user-state writer can be passed in: each field is a narrow
// interface, TestRiskServiceCannotWriteServiceState pins each method set, and
// it forbids type assertions in this package, so a wider value handed in
// cannot be recovered either.
type Deps struct {
	// Users lists the accounts to judge. Required.
	Users UserLister
	// Store holds the verdicts. Required.
	Store SignalStore
	// Settings resolves the global settings (the panel's timezone, the
	// rollup's retention) and each group's policy. Required.
	Settings ports.ScopedSettings
	// Traffic is the hourly rollup. Nil: usage_shift is not computed.
	Traffic HourlyReader
	// Now is the clock. Nil: time.Now.
	Now func() time.Time
	// SubLogs is the subscription fetch log, read as one streamed window.
	// Nil: sub_spread, devices and login_country are not computed (the
	// last because its known countries are the ones the fetches
	// established).
	SubLogs FetchScanner
	// Geo places the window's sources. Nil: nothing can be placed, and the
	// place signals read unknown/geo_unavailable — never clean.
	Geo GeoResolver
	// IsInfra reports PSP's own node and relay addresses, which say where
	// the relay is, not the user. A function, not the traffic service: the
	// worker gets the one question and nothing else that service can do.
	// Nil: nothing is infrastructure.
	IsInfra func(netip.Addr) bool
	// InfraLoaded reports whether the infrastructure set has been built
	// once. Until it has, the place signals are skipped (their rows kept):
	// judged against a set not collected yet, every relayed account would
	// read as fetching from the relay's province. Nil: treated as loaded,
	// for tests and deployments without the set.
	InfraLoaded func() bool
	// AuthEvents is the authentication-event log, read for its successful
	// logins. Nil: login_country is not computed.
	AuthEvents LoginLister
	// LandingAddrs reports PSP's landing-node addresses — each enabled
	// node's own server, not the relays in front of it. A login from a
	// country one of them is in is skipped: an account holder's browser
	// often reaches the panel through their own proxy, whose egress is the
	// landing's. A function, like IsInfra. Nil: no country is a node
	// country.
	LandingAddrs func() []netip.Addr
}

// UserLister pages through the accounts (ports.UserRepo's List, alone).
type UserLister interface {
	List(ctx context.Context, f ports.UserFilter) ([]*domain.User, int64, error)
}

// SignalStore is the worker's view of risk_signals: write this run's rows,
// drop what deleted accounts left. It cannot read, so the worker never
// judges from its own previous output. Which rows changed their attention
// level is the store's business (sqlstore.RiskSignalRepo.Save records them
// in flag_records as part of the same write), for the same reason: finding
// a change needs the previous state, and the worker must not read it.
type SignalStore interface {
	Save(ctx context.Context, rows []domain.RiskSignal) error
	PurgeOrphans(ctx context.Context) (int64, error)
}

// HourlyReader is the hourly traffic rollup — the one traffic source that
// reaches back over usage_shift's series (35 days shipped, up to 70
// configured; raw snapshots are kept about a week).
type HourlyReader interface {
	ListHourlyByUser(ctx context.Context, userID int64, since, until time.Time) ([]domain.HourlyTraffic, error)
	SumHourlyAllUsers(ctx context.Context, since, until time.Time) ([]domain.HourlyTraffic, error)
}

// FetchScanner streams the subscription fetch log from since on, batch rows
// at a time (ports.SubLogRepo's ScanSince, alone). A week of fetches can be
// hundreds of thousands of rows; the worker folds each batch into its
// per-account window and keeps none of them.
type FetchScanner interface {
	ScanSince(ctx context.Context, since time.Time, batch int, fn func([]domain.SubLog) error) error
}

// LoginLister pages through the authentication-event log
// (ports.AuthEventRepo's List, alone). It cannot write, and it cannot reach
// the login guard's counters either.
type LoginLister interface {
	List(ctx context.Context, f ports.AuthEventFilter) ([]*domain.AuthEvent, int64, error)
}

// GeoResolver places addresses (the geo service's read side). Available is
// asked separately because an empty lookup means two opposite things — "no
// database" and "nothing placeable" — and only the second is evidence.
type GeoResolver interface {
	Lookup(ctx context.Context, ips []string) map[string]domain.GeoLocation
	Available(ctx context.Context) bool
}

// Service recomputes the risk signals. It holds no state between runs: each
// run judges from whole windows, and the store is the only memory.
type Service struct {
	d   Deps
	now func() time.Time
}

// New builds the worker. It does not start anything; the composition root
// runs RefreshOnce on its own loop.
func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{d: d, now: now}
}

// userPageSize is how many accounts one List call asks for, the same page
// the traffic poll walks the fleet with.
const userPageSize = 100

// Refresh outcomes, psp_risk_refresh_total's label values, in precedence
// order error > partial > infra_pending > ok.
const (
	outcomeOK           = "ok"
	outcomePartial      = "partial"
	outcomeInfraPending = "infra_pending"
	outcomeError        = "error"
)

// refresh is one run's working state.
type refresh struct {
	now    time.Time
	loc    *time.Location
	global ports.UISettings
	// rt is the fleet-wide runtime, resolved once per run from the global
	// settings: the fetch window and the login lookback as CONFIGURED. The
	// sub-log and auth-event retentions shorten each where its log is read
	// (windowDays, loginLookbackDays), never here — the groups' policies
	// are bounded by rt, and a retention-shortened bound would hide
	// retention_short.
	rt    domain.RiskRuntime
	users []*domain.User
	// policies holds each readable group's policies. A group whose settings
	// could not be read is absent, and its accounts get no rows.
	policies map[int64]groupPolicy
	rows     []domain.RiskSignal
	partial  bool
	// infraPending: the place signals were skipped because the
	// infrastructure set has not been built yet.
	infraPending bool
}

// groupPolicy is what one group's settings resolve to: the risk policy, and
// the concurrent-location policy sub_spread reuses (V3-D3) — its scope,
// region tolerance, exemption and placed ratio.
type groupPolicy struct {
	risk domain.RiskPolicy
	geo  domain.GeoAnomalyPolicy
}

// RefreshOnce recomputes every signal for every account and saves the rows.
//
// Never called concurrently (one loop goroutine), and it spawns no
// goroutines. It returns an error, and writes nothing, when the run cannot
// start (no settings, no user list), when the save fails, or when ctx is
// cancelled mid-run; a source that fails part-way costs only the rows it
// feeds, and the run is counted partial.
func (s *Service) RefreshOnce(ctx context.Context) (err error) {
	started := time.Now()
	outcome := outcomeError
	defer func() { metrics.RiskRefreshTotal.With(outcome).Inc() }()

	if s == nil || s.d.Users == nil || s.d.Store == nil || s.d.Settings == nil {
		return fmt.Errorf("risk refresh: %w: users, store and settings are required", domain.ErrUnavailable)
	}
	r := &refresh{now: s.now()}
	r.global, err = s.d.Settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return fmt.Errorf("risk refresh: load settings: %w", err)
	}
	r.loc = paneltz.LocationOf(r.global.Timezone)
	r.rt = domain.RiskRuntimeFromSettings(r.global.RiskRuntimeSettings())
	if r.users, err = s.listUsers(ctx); err != nil {
		return fmt.Errorf("risk refresh: %w", err)
	}

	// Deleting an account cascades nothing, so the rows it left would stay
	// forever; every run removes them. A failure costs only tidiness — every
	// read JOINs users and never shows one — so the run goes on.
	if n, perr := s.d.Store.PurgeOrphans(ctx); perr != nil {
		if ctx.Err() == nil {
			log.Warn("risk signals: purging rows of deleted accounts failed", "err", perr)
		}
	} else if n > 0 {
		log.Info("risk signals: purged rows of deleted accounts", "rows", n)
	}

	if err := s.loadPolicies(ctx, r); err != nil {
		return err
	}
	if s.d.Traffic != nil {
		if err := s.usageShift(ctx, r); err != nil {
			return err
		}
	}

	// The place signals judge where the week's fetches came from, so they
	// wait for the infrastructure set: before its first build it is empty
	// because nothing was collected yet, and every account behind a relay
	// would read as fetching from the relay's province. The loop's first run
	// is minutes after the set's, so this is a guard, not a schedule. Their
	// stored rows stay as they were meanwhile. The device count places
	// nothing, so the window is read either way and devices does not wait.
	infraReady := s.d.InfraLoaded == nil || s.d.InfraLoaded()
	if s.d.SubLogs != nil {
		if !infraReady {
			r.infraPending = true
			log.Info("infrastructure addresses not loaded yet; place-based risk signals skipped this run")
		}
		// A window read part-way is not judged: a week missing its last
		// batches reads as fewer provinces, fewer days and fewer devices.
		// The kinds it feeds keep their previous rows.
		window, err := s.readWindow(ctx, r)
		switch {
		case err == nil:
			if err := s.devices(ctx, r, window); err != nil {
				return err
			}
			if infraReady {
				pl := s.placeWindow(ctx, r, window)
				if err := s.subSpread(ctx, r, window, pl); err != nil {
					return err
				}
				// login_country measures logins against the countries the
				// fetches established, so it runs only here: on a window
				// read in full and placed against a loaded infrastructure
				// set. Judged with an empty or partial known set, every
				// login from home would lean toward "new".
				if s.d.AuthEvents != nil {
					if err := s.loginCountry(ctx, r, pl); err != nil {
						return err
					}
				}
			}
		case ctx.Err() != nil:
			return fmt.Errorf("risk refresh: %w", ctx.Err())
		default:
			r.partial = true
			log.Warn("risk signals: subscription fetch log unreadable; the fetch-based signals keep their previous rows", "err", err)
		}
	}

	if len(r.rows) > 0 {
		if err := s.d.Store.Save(ctx, r.rows); err != nil {
			return fmt.Errorf("risk refresh: save: %w", err)
		}
	}
	flagged := 0
	for _, row := range r.rows {
		if row.State == domain.GeoStateFlagged {
			flagged++
		}
	}
	switch {
	case r.partial:
		outcome = outcomePartial
	case r.infraPending:
		outcome = outcomeInfraPending
	default:
		outcome = outcomeOK
	}
	// Counts only: never an address, a user agent, a label or a device id.
	log.Info("risk signals refreshed",
		"outcome", outcome, "users", len(r.users), "rows", len(r.rows), "flagged", flagged,
		"infra_ready", infraReady, "ms", time.Since(started).Milliseconds())
	return nil
}

// listUsers pages through every account, as the traffic poll does. The
// store refuses a batch that names one (user, kind) twice, so an account
// that shows up on two pages — a concurrent change shifting the offsets —
// is kept once rather than failing the whole save.
func (s *Service) listUsers(ctx context.Context) ([]*domain.User, error) {
	var out []*domain.User
	seen := map[int64]bool{}
	for page := 1; ; page++ {
		users, total, err := s.d.Users.List(ctx, ports.UserFilter{
			Pagination: ports.Pagination{Page: page, PageSize: userPageSize},
		})
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		for _, u := range users {
			if u != nil && !seen[u.ID] {
				seen[u.ID] = true
				out = append(out, u)
			}
		}
		if int64(page*userPageSize) >= total || len(users) == 0 {
			return out, nil
		}
	}
}

// loadPolicies resolves each group's policies once. A group whose
// settings cannot be read is left out: judging its accounts with the global
// or the default policy would be judging them with a policy their admin may
// have overridden — a switched-off signal switched back on. Its accounts
// keep their previous rows, and the run is partial.
func (s *Service) loadPolicies(ctx context.Context, r *refresh) error {
	r.policies = map[int64]groupPolicy{}
	failed := map[int64]bool{}
	for _, u := range r.users {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("risk refresh: %w", err)
		}
		gid := u.GroupID
		if _, ok := r.policies[gid]; ok || failed[gid] {
			continue
		}
		set, err := s.d.Settings.LoadForGroup(ctx, gid, ports.UISettings{})
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("risk refresh: %w", ctx.Err())
			}
			failed[gid] = true
			r.partial = true
			log.Warn("risk signals: group settings unreadable; its accounts keep their previous rows", "group_id", gid, "err", err)
			continue
		}
		// The geo policy through the same mapping and the same defaults
		// the traffic poll judges with, so a group's tolerance means one
		// thing on both sides. The risk policy is bounded by the fleet's
		// configured runtime — a min_days no window could satisfy, or a
		// hold longer than the log is read, would describe what the worker
		// never looks at.
		r.policies[gid] = groupPolicy{
			risk: domain.RiskPolicyFromSettings(set.RiskPolicySettings()).Bounded(r.rt),
			geo:  domain.GeoPolicyFromSettings(set.GeoPolicySettings()),
		}
	}
	return nil
}

// addVerdict appends one verdict as a row. A nil evidence pointer is stored as
// NULL. Evidence that will not encode costs its own row (the store keeps the
// previous one) rather than the run.
func addVerdict[E any](r *refresh, userID int64, kind domain.RiskKind, v domain.RiskVerdict, ev *E) {
	row := domain.RiskSignal{UserID: userID, Kind: kind, State: v.State, Code: v.Code}
	if ev != nil {
		raw, err := json.Marshal(ev)
		if err != nil {
			r.partial = true
			log.Warn("risk signals: evidence did not encode; the previous row is kept", "user_id", userID, "kind", kind, "err", err)
			return
		}
		row.Evidence = raw
	}
	r.rows = append(r.rows, row)
}
