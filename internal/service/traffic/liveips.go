package traffic

import (
	"context"
	"sort"
	"time"

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

// SetGeoPolicy installs the fallback policy: the one judged with when no
// settings are wired, when a group's settings cannot be read, or for a client
// owner missing from the poll's user list. When they can be read, the stored
// global values with that group's overrides on top replace it whole (see
// geoPolicyCache below); there is no per-user layer. A fallback standing in
// for an unread group is never used to time a lift (see geoPolicyEntry).
func (s *Service) SetGeoPolicy(p domain.GeoAnomalyPolicy) { s.geoPolicy = p }

// GeoStreakStore persists the little between-poll state that makes a verdict
// stable instead of jittery. Kept behind an interface so the poll can run
// with an in-memory store in tests and a real one in production, and so a
// deployment that has not wired storage still gets correct single-sample
// behaviour rather than a crash.
type GeoStreakStore interface {
	Load(ctx context.Context) (map[int64]domain.GeoRecord, error)
	Save(ctx context.Context, records map[int64]domain.GeoRecord) error
}

// SetGeoStreakStore late-binds streak persistence.
func (s *Service) SetGeoStreakStore(st GeoStreakStore) { s.geoStreaks = st }

// liveIPInput is one cycle's worth of what observeLiveIPs needs. A struct
// rather than a parameter list because the poll and the tests fill different
// subsets: the direct tests leave the policy cache, the spacing and the clock
// unset and get the documented defaults.
type liveIPInput struct {
	users    []*domain.User
	clients  []*domain.PSPClient
	panelIDs map[int64]struct{}
	// read returns one panel's live-IP answer for this cycle.
	read func(panelID int64) domain.PanelLiveIPs
	// ignore is the admin's global ignore list, raw.
	ignore string
	// policies resolves each user's effective policy; nil builds one from
	// users.
	policies *geoPolicyCache
	// minSpacing: a user judged less than this long ago is not re-judged.
	// 0 disables the guard.
	minSpacing time.Duration
	// now is the judging instant; zero means time.Now().
	now time.Time
}

// geoPolicyCache resolves the location policy per GROUP, not per user, for
// one poll.
//
// The knobs are group-overridable (ports.OverridableScopeKeys), so every
// member of a group shares one answer, and a per-user settings load would be
// one round trip per user per poll for a value that cannot differ within the
// group. Built fresh for each cycle, so an admin's change takes effect on the
// next poll rather than on restart. Used only inside one PollOnce, from one
// goroutine, so it takes no lock; it keeps that poll's context because every
// lookup belongs to it.
//
// Keyed by GroupID, and ONLY for users in the poll's list: 0 is a real key
// ("no group", common for SSO and legacy rows), so an owner the list does not
// contain must never be filed under it (see lookup).
type geoPolicyCache struct {
	s      *Service
	ctx    context.Context
	byUser map[int64]*domain.User
	byKey  map[int64]geoPolicyEntry
}

// geoPolicyEntry is one group's policy for the poll, and whether it IS that
// group's policy.
//
// resolved is false when the policy is only the deployment fallback standing
// in for a value this poll could not learn: the group's settings read failed,
// or the owner is not in the poll's user list and so has no group at all.
// Judging may use the stand-in: production's fallback is the shipped default,
// whose suspension is off, so judging on it can hold a suspension back but
// never cause one. A time box may not: the fallback's 60 minutes would end a
// group's longer suspension early, and nothing puts it back (see
// liftDueGeoSuspensions).
//
// With no settings wired the fallback IS the configuration, so it counts as
// resolved.
type geoPolicyEntry struct {
	policy   domain.GeoAnomalyPolicy
	resolved bool
}

func (s *Service) newGeoPolicyCache(ctx context.Context, users []*domain.User) *geoPolicyCache {
	byUser := make(map[int64]*domain.User, len(users))
	for _, u := range users {
		if u != nil {
			byUser[u.ID] = u
		}
	}
	return &geoPolicyCache{s: s, ctx: ctx, byUser: byUser, byKey: map[int64]geoPolicyEntry{}}
}

// forUser is the effective policy for one user: the stored global values
// with that user's group overrides on top, or the deployment fallback when
// no settings are wired, the user is unknown, or the read fails. Callers that
// must tell the fallback from a real answer use lookup.
func (c *geoPolicyCache) forUser(uid int64) domain.GeoAnomalyPolicy {
	return c.lookup(uid).policy
}

// lookup is forUser with its provenance: see geoPolicyEntry.
func (c *geoPolicyCache) lookup(uid int64) geoPolicyEntry {
	u := c.byUser[uid]
	if u == nil {
		// The owner of a shared client who is missing from this poll's user
		// list: a user row removed without its psp_clients, or one the
		// OFFSET-paged list skipped when a row was deleted mid-read. There
		// is no group to resolve, so it gets the fallback, and it is not
		// cached. Filed under group 0, as it once was, it decided the policy
		// of every no-group user for the rest of the poll whenever the map
		// order reached it first: suspension off, the shipped tolerances,
		// an armed ban streak wiped, and in Phase 4 a stored 24-hour box
		// lifted on the fallback's 60 minutes. There is no read behind it,
		// so there is nothing to cache either.
		return geoPolicyEntry{policy: c.s.geoPolicy}
	}
	if e, ok := c.byKey[u.GroupID]; ok {
		return e
	}
	e := geoPolicyEntry{policy: c.s.geoPolicy, resolved: c.s.settings == nil}
	if c.s.settings != nil {
		// LoadForUser is the group's overrides on top of the global
		// values, whole value by whole value — there is no per-user
		// layer — and GeoPolicyFromSettings then reads a 0 as "never
		// configured", not as zero tolerance.
		//
		// Every per-group geo knob goes through here; the ignore list
		// does not, because it is global only and is not part of the
		// judging policy — it decides which addresses are judged at all.
		if set, err := c.s.settings.LoadForUser(c.ctx, u, ports.UISettings{}); err == nil {
			e.resolved = true
			e.policy = domain.GeoPolicyFromSettings(domain.GeoPolicySettings{
				Scope:              set.GeoAnomalyScope,
				MaxPlaces:          set.GeoAnomalyMaxPlaces,
				MaxRegions:         set.GeoAnomalyMaxRegions,
				MaxCities:          set.GeoAnomalyMaxCities,
				FlagAfterPolls:     set.GeoAnomalyFlagAfterPolls,
				ClearAfterPolls:    set.GeoAnomalyClearAfterPolls,
				MinPlacedRatio:     set.GeoAnomalyMinPlacedRatio,
				CoTravel:           set.GeoAnomalyCoTravel,
				AllowAnywhere:      set.GeoAnomalyAllowAnywhere,
				BanEnabled:         set.GeoAnomalyBanEnabled,
				BanMaxCountries:    set.GeoAnomalyBanMaxCountries,
				BanMaxRegions:      set.GeoAnomalyBanMaxRegions,
				BanMaxCities:       set.GeoAnomalyBanMaxCities,
				BanAfterPolls:      set.GeoAnomalyBanAfterPolls,
				BanDurationMinutes: set.GeoAnomalyBanDurationMinutes,
			})
		} else {
			// Fall back to the process default rather than to a zero
			// policy: a zero MaxPlaces would flag every connected user.
			// Cached unresolved for the rest of the poll: one Warn per
			// group, and Phase 4 reads the same answer Phase 1b judged
			// on (and knows not to time a suspension by it).
			log.Warn("geo policy: could not read a group's settings; this poll uses the deployment default for it",
				"user_id", uid, "group_id", u.GroupID, "err", err)
		}
	}
	c.byKey[u.GroupID] = e
	return e
}

// observeLiveIPs folds this cycle's per-panel live-IP reads into one row per
// user, judges each against the effective policy, and records the result.
//
// What is judged is narrower than what the panels report, on purpose:
//
//   - only CONCURRENT addresses: the upstream remembers an address for 30
//     minutes after its stream closed, so its list read as "at once" puts one
//     commuter in several cities. domain.FreshLiveIPs keeps the addresses
//     seen within a node's latest scans, against the reference map below;
//   - only addresses that are not infrastructure or noise: carrier-grade NAT
//     and private ranges, the admin's ignore list, PSP's own node and relay
//     addresses, and an exit three or more accounts share at once are set
//     aside (domain.ClassifyAddresses) and counted rather than placed;
//   - at most once per half poll interval per user (in.minSpacing), so a
//     manual poll cannot turn clicks into samples.
//
// It changes no user's state and writes to no panel. What it does beyond the
// verdict is hand back the automatic suspensions that are due (only where a
// group has armed them; off by default), for PollOnce to apply at the end of
// the cycle (enforceGeo). Which of them are handed back is decided here,
// before the streaks are saved, because that decision is also a streak write:
// see collectGeoBans.
//
// Never fails the poll. Traffic metering is what PollOnce exists for, and a
// missing geo database or an unreadable panel must not cost the cycle its
// primary job.
func (s *Service) observeLiveIPs(ctx context.Context, in liveIPInput) []geoBan {
	if len(in.clients) == 0 || in.read == nil {
		return nil
	}

	owners := make(map[domain.ClientKey]int64, len(in.clients))
	for _, c := range in.clients {
		if c == nil || c.Email == "" {
			continue
		}
		owners[domain.NewClientKey(c.PanelID, c.Email)] = c.UserID
	}
	if len(owners) == 0 {
		return nil
	}
	now := in.now
	if now.IsZero() {
		now = time.Now()
	}

	panels := make([]domain.PanelLiveIPs, 0, len(in.panelIDs))
	for pid := range in.panelIDs {
		p := in.read(pid)
		// The owner map is keyed by panel ID; an answer filed under any
		// other ID would silently match nobody.
		p.PanelID = pid
		panels = append(panels, p)
	}
	panels = s.freshLiveIPs(panels)

	agg := domain.AggregateLiveIPsByUser(panels, owners)

	// A bad entry in the ignore list must not switch the whole list off:
	// the valid entries still apply, and the Warn names the rest. The
	// settings PUT rejects bad entries up front, so this is a value that
	// predates that check or was written around it.
	ignore, err := domain.ParseGeoIgnoreList(in.ignore)
	if err != nil {
		log.Warn("live-ip observe: the geo ignore list has invalid entries; applying the valid ones", "err", err)
	}
	exclusions := domain.AddressExclusions{
		Internal:       true,
		Ignore:         ignore,
		SharedMinUsers: domain.SharedExitMinUsers,
	}
	if s.infra != nil {
		// A read lock and a map lookup per source; the DNS behind it ran
		// in the refresh loop, never here.
		exclusions.Infra = s.infra.Contains
	}
	addrs := domain.ClassifyAddresses(agg, exclusions)

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
	prev := map[int64]domain.GeoRecord{}
	if s.geoStreaks != nil {
		loaded, err := s.geoStreaks.Load(ctx)
		if err != nil {
			log.Warn("live-ip observe: could not load geo streaks; this cycle judges without history", "err", err)
		} else if loaded != nil {
			prev = loaded
		}
	}

	pc := in.policies
	if pc == nil {
		pc = s.newGeoPolicyCache(ctx, in.users)
	}

	next := make(map[int64]domain.GeoRecord, len(agg))
	var due []geoBan
	var incomplete, spaced int
	for uid, u := range agg {
		// Sample spacing. The staff "poll now" calls PollOnce directly, and
		// without this every click would be a sample: flag_after_polls in as
		// many clicks, and for a PSP-native node (no timestamps, so nothing
		// else notices the data did not change) a sustained streak in
		// seconds. A user judged less than half an interval ago is left
		// exactly as stored — no row write, no metric — so the scheduled
		// polls, an interval apart, are what count. A PSP clock stepping
		// backwards also reads as "too soon" until it passes the stored
		// time again; that pauses judging and never accuses anyone.
		if in.minSpacing > 0 {
			if last := prev[uid].UpdatedAtMS; last > 0 && now.Sub(time.UnixMilli(last)) < in.minSpacing {
				spaced++
				continue
			}
		}

		metrics.UserLiveIPs.Observe(float64(u.Count()))
		if !u.Complete() {
			incomplete++
		}

		policy := pc.forUser(uid)
		a := addrs[uid]

		obs := domain.ObserveGeo(policy, a, lookup, geoAvailable)
		v := domain.EvaluateGeo(policy, obs, prev[uid].Streak)
		next[uid] = domain.GeoRecord{
			UserID:     uid,
			Streak:     v.Streak,
			State:      v.State,
			Reason:     v.Reason,
			Places:     v.Places,
			LiveIPs:    u.Count(),
			Concurrent: obs.Placed + obs.Unplaced,
			Excluded:   obs.Excluded.Total(),
			Evidence:   domain.GeoEvidenceFrom(obs),
			Complete:   u.Complete(),
		}
		if v.BanDue {
			due = append(due, geoBan{UserID: uid, Tier: v.BanTier, Reason: v.BanReason, Spread: v.BanSpread})
		}
		metrics.GeoVerdictTotal.With(string(v.State)).Inc()
		metrics.UserConcurrentIPs.Observe(float64(obs.Placed + obs.Unplaced))
		if a.Stale > 0 {
			metrics.LiveIPStaleTotal.Add(int64(a.Stale))
		}
		countExclusions(a.Excluded)
		// This sample was over when the flag streak advanced on it: a
		// latched flag with an under-sample has Over 0, and the frozen
		// states (idle, unknown) never reach Suspect or Flagged.
		sampleOver := v.Streak.Over > 0 && (v.State == domain.GeoStateSuspect || v.State == domain.GeoStateFlagged)
		if sampleOver {
			metrics.GeoOverTierTotal.With(string(v.Streak.Tier)).Inc()
		}

		// Log only the states an operator would want to see, and only the
		// sustained one at Warn. Suspect at Info keeps the ramp visible
		// without making a noisy fleet unreadable.
		switch v.State {
		case domain.GeoStateFlagged:
			log.Warn("concurrent-location anomaly",
				"user_id", uid, "tier", v.Streak.Tier, "places", v.Places, "reason", v.Reason,
				"live_ips", u.Count(), "concurrent", obs.Placed+obs.Unplaced,
				"excluded", obs.Excluded.Total(), "complete", u.Complete())
		case domain.GeoStateSuspect:
			log.Info("concurrent-location anomaly building",
				"user_id", uid, "tier", v.Streak.Tier, "places", v.Places, "reason", v.Reason)
		}
	}

	if spaced > 0 {
		metrics.GeoSamplesSpacedTotal.Add(int64(spaced))
		log.Info("live-ip observe: users judged less than half a poll interval ago were not judged again",
			"users", spaced, "min_spacing", in.minSpacing.String())
	}
	if incomplete > 0 {
		metrics.LiveIPUsersIncompleteTotal.Add(int64(incomplete))
		log.Warn("live-ip observe: some users' totals are floors, not totals",
			"users", incomplete, "panels", len(panels))
	}

	bans := collectGeoBans(due, in.users, next, pc, now)

	if s.geoStreaks != nil {
		if err := s.geoStreaks.Save(ctx, next); err != nil {
			log.Warn("live-ip observe: could not persist geo streaks; hysteresis restarts next cycle", "err", err)
		}
	}
	return bans
}

// collectGeoBans turns this cycle's due suspensions into the ones PollOnce
// will try to apply, and fixes up the streaks that decision affects. It runs
// before the save because it edits next.
//
// A due verdict has already CONSUMED its ban streak (EvaluateGeo resets it,
// so a suspended user who goes idle cannot be re-suspended by their first
// over-sample after the lift). So every due ban ends here in exactly one of
// three ways, and none is silently lost:
//
//   - not eligible now (held by another reason, expired, over quota, in an
//     emergency window, account disabled; see geoBanEligible): stays
//     consumed and is counted skipped_held. These are handled BEFORE the cap
//     so a crowd of held users cannot use up its slots;
//   - eligible and inside geoMaxSuspensionsPerPoll, lowest user ID first so
//     the choice is deterministic: returned;
//   - eligible but over the cap: deferred. The consumption is undone by
//     putting the streak back AT the threshold, so the user's next
//     over-sample makes it due again; counted deferred.
//
// Eligibility is judged on the users as listed at the top of the poll;
// enforceGeo re-checks it on the same users after the quota pass.
func collectGeoBans(due []geoBan, users []*domain.User, next map[int64]domain.GeoRecord, pc *geoPolicyCache, now time.Time) []geoBan {
	if len(due) == 0 {
		return nil
	}
	byID := make(map[int64]*domain.User, len(users))
	for _, u := range users {
		if u != nil {
			byID[u.ID] = u
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].UserID < due[j].UserID })

	var bans []geoBan
	var held, deferred int
	for _, b := range due {
		if !geoBanEligible(byID[b.UserID], now) {
			held++
			continue
		}
		if len(bans) < geoMaxSuspensionsPerPoll {
			// The row as it will be saved: only deferred users' rows are
			// edited below, so this one is final.
			b.Record = next[b.UserID]
			bans = append(bans, b)
			continue
		}
		// The same threshold the verdict was judged against: group
		// policies come through GeoPolicyFromSettings, and production's
		// fallback is the shipped default, both already sanitized. (A raw
		// fallback set with 0 here would restore 0, which only makes the
		// next suspension later, never sooner.)
		rec := next[b.UserID]
		rec.Streak.BanOver = pc.forUser(b.UserID).BanAfterPolls
		next[b.UserID] = rec
		deferred++
	}
	geoAutoCount("skipped_held", held)
	geoAutoCount("deferred", deferred)
	if deferred > 0 {
		log.Warn("geo auto-suspension: more suspensions due than one poll applies; the rest are deferred to their next over-sample",
			"eligible", len(bans)+deferred, "cap", geoMaxSuspensionsPerPoll, "deferred", deferred)
	}
	return bans
}

// freshLiveIPs marks which sightings were live at poll time and advances the
// per-node reference map, under its lock.
//
// The merge is by max, never assignment. Two polls can overlap, and the
// slower one may finish with the OLDER reference; letting it win would make
// the next poll read a batch nobody rescanned as "advanced" and replay it as
// a fresh sample. Pure domain logic does the deciding; this only owns the
// state between polls.
func (s *Service) freshLiveIPs(panels []domain.PanelLiveIPs) []domain.PanelLiveIPs {
	s.liveRefsMu.Lock()
	defer s.liveRefsMu.Unlock()
	if s.liveRefs == nil {
		s.liveRefs = map[domain.NodeRef]int64{}
	}
	// FreshLiveIPs only reads prev, and the lock is held throughout, so
	// the live map is passed as is.
	out, next := domain.FreshLiveIPs(panels, s.liveRefs)
	for k, v := range next {
		if v > s.liveRefs[k] {
			s.liveRefs[k] = v
		}
	}
	return out
}

// countExclusions adds one user's excluded sources to the per-reason
// counter. Zero reasons are skipped so a label appears only once it has
// something to say.
func countExclusions(e domain.GeoExcluded) {
	for _, r := range []struct {
		reason string
		n      int
	}{
		{"shared", e.Shared},
		{"listed", e.Listed},
		{"infra", e.Infra},
		{"internal", e.Internal},
	} {
		if r.n > 0 {
			metrics.LiveIPExcludedTotal.With(r.reason).Add(int64(r.n))
		}
	}
}
