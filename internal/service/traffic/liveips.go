package traffic

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// GeoResolver is what this package needs from the geo service: place a batch
// of addresses, and say whether it is in a position to place anything at all.
//
// Two methods rather than one because "the database is off" and "none of
// these addresses resolved" are different facts that produce identical
// output. Collapsing them is how a stale database silently reads as a fleet
// with nobody sharing.
type GeoResolver interface {
	Lookup(ctx context.Context, ips []string) map[string]domain.GeoLocation
	Available(ctx context.Context) bool
}

// SetGeoResolver late-binds location lookup, matching SetPSPClientRepo. Nil
// is a supported state: the aggregate still runs and every verdict is
// Unknown, which is the honest answer when nothing can be placed.
func (s *Service) SetGeoResolver(g GeoResolver) { s.geo = g }

// SetGeoPolicy installs the deployment-wide default policy. Per-group and
// per-user overrides layer on top of it; see domain.ResolveGeoPolicy.
func (s *Service) SetGeoPolicy(p domain.GeoAnomalyPolicy) { s.geoPolicy = p }

// GeoStreakStore persists the little between-poll state that makes a verdict
// stable instead of jittery. Kept behind an interface so the poll can run
// with an in-memory store in tests and a real one in production, and so a
// deployment that has not wired storage still gets correct single-sample
// behaviour rather than a crash.
type GeoStreakStore interface {
	Load(ctx context.Context) (map[int64]domain.GeoStreak, error)
	Save(ctx context.Context, streaks map[int64]domain.GeoStreak) error
}

// SetGeoStreakStore late-binds streak persistence.
func (s *Service) SetGeoStreakStore(st GeoStreakStore) { s.geoStreaks = st }

// observeLiveIPs folds this cycle's per-panel live-IP reads into one row per
// user, judges each against the effective policy, and records the result.
//
// OBSERVATION ONLY. It changes no user's state and writes to no panel. The
// distribution it produces is the input to deciding whether any automatic
// response is safe to arm — arming one against an unknown false-positive
// rate is how a fleet locks out paying customers.
//
// Never fails the poll. Traffic metering is what PollOnce exists for, and a
// missing geo database or an unreadable panel must not cost the cycle its
// primary job.
func (s *Service) observeLiveIPs(
	ctx context.Context,
	users []*domain.User,
	clients []*domain.PSPClient,
	panelIPs func(panelID int64) (map[string][]string, error),
	panelIDs map[int64]struct{},
) {
	if len(clients) == 0 || panelIPs == nil {
		return
	}

	owners := make(map[domain.ClientKey]int64, len(clients))
	for _, c := range clients {
		if c == nil || c.Email == "" {
			continue
		}
		owners[domain.NewClientKey(c.PanelID, c.Email)] = c.UserID
	}
	if len(owners) == 0 {
		return
	}

	panels := make([]domain.PanelLiveIPs, 0, len(panelIDs))
	for pid := range panelIDs {
		ips, err := panelIPs(pid)
		panels = append(panels, domain.PanelLiveIPs{PanelID: pid, ByEmail: ips, Err: err})
	}

	agg := domain.AggregateLiveIPsByUser(panels, owners)

	geoAvailable := s.geo != nil && s.geo.Available(ctx)
	var lookup domain.GeoLookup
	if s.geo != nil {
		lookup = func(ips []string) map[string]domain.GeoLocation {
			return s.geo.Lookup(ctx, ips)
		}
	}

	// Load the streaks once. A failure here degrades to "every user starts
	// from a clean streak this cycle", which under-reports (nobody reaches
	// the flag threshold) rather than over-reports — the safe direction when
	// the alternative is accusing people on state we could not read.
	streaks := map[int64]domain.GeoStreak{}
	if s.geoStreaks != nil {
		loaded, err := s.geoStreaks.Load(ctx)
		if err != nil {
			log.Warn("live-ip observe: could not load geo streaks; this cycle judges without history", "err", err)
		} else if loaded != nil {
			streaks = loaded
		}
	}

	// Resolve the policy per GROUP, not per user.
	//
	// The knobs are group-overridable (ports.OverridableScopeKeys), so every
	// member of a group shares one answer, and a per-user settings load would
	// be one round trip per user per poll for a value that cannot differ
	// within the group. Cached for the cycle; a fresh poll re-reads, so an
	// admin's change takes effect on the next cycle rather than on restart.
	byUser := make(map[int64]*domain.User, len(users))
	for _, u := range users {
		if u != nil {
			byUser[u.ID] = u
		}
	}
	policyCache := map[int64]domain.GeoAnomalyPolicy{}
	policyFor := func(uid int64) domain.GeoAnomalyPolicy {
		u := byUser[uid]
		key := int64(0)
		if u != nil {
			key = u.GroupID
		}
		if p, ok := policyCache[key]; ok {
			return p
		}
		p := s.geoPolicy
		if s.settings != nil && u != nil {
			// LoadForUser already layers user > group > global, which is the
			// same precedence the traffic and connection limits use.
			if set, err := s.settings.LoadForUser(ctx, u, ports.UISettings{}); err == nil {
				p = domain.GeoPolicyFromSettings(domain.GeoPolicySettings{
					Scope:           set.GeoAnomalyScope,
					MaxPlaces:       set.GeoAnomalyMaxPlaces,
					FlagAfterPolls:  set.GeoAnomalyFlagAfterPolls,
					ClearAfterPolls: set.GeoAnomalyClearAfterPolls,
					MinPlacedRatio:  set.GeoAnomalyMinPlacedRatio,
					CoTravel:        set.GeoAnomalyCoTravel,
					AllowAnywhere:   set.GeoAnomalyAllowAnywhere,
				})
			} else {
				// Fall back to the process default rather than to a zero
				// policy: a zero MaxPlaces would flag every connected user.
				log.Warn("live-ip observe: could not resolve the location policy; using the deployment default",
					"user_id", uid, "err", err)
			}
		}
		policyCache[key] = p
		return p
	}

	next := make(map[int64]domain.GeoStreak, len(agg))
	var incomplete int
	for uid, u := range agg {
		metrics.UserLiveIPs.Observe(float64(u.Count()))
		if !u.Complete() {
			incomplete++
		}

		policy := policyFor(uid)

		obs := domain.ObserveGeo(policy, u, lookup, geoAvailable)
		v := domain.EvaluateGeo(policy, obs, streaks[uid])
		next[uid] = v.Streak
		metrics.GeoVerdictTotal.With(string(v.State)).Inc()

		// Log only the states an operator would want to see, and only the
		// sustained one at Warn. Suspect at Info keeps the ramp visible
		// without making a noisy fleet unreadable.
		switch v.State {
		case domain.GeoStateFlagged:
			log.Warn("concurrent-location anomaly",
				"user_id", uid, "places", v.Places, "reason", v.Reason,
				"live_ips", u.Count(), "complete", u.Complete())
		case domain.GeoStateSuspect:
			log.Info("concurrent-location anomaly building",
				"user_id", uid, "places", v.Places, "reason", v.Reason)
		}
	}

	if incomplete > 0 {
		metrics.LiveIPUsersIncompleteTotal.Add(int64(incomplete))
		log.Warn("live-ip observe: some users' totals are floors, not totals",
			"users", incomplete, "panels", len(panels))
	}

	if s.geoStreaks != nil {
		if err := s.geoStreaks.Save(ctx, next); err != nil {
			log.Warn("live-ip observe: could not persist geo streaks; hysteresis restarts next cycle", "err", err)
		}
	}
}
