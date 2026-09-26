package traffic

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The automatic location suspension (reason geo_auto): off by default, armed
// per group, time-boxed, and never written over anyone else's decision.
//
// The domain decides WHEN a ban is due (EvaluateGeo's BanDue, on its own
// tolerances and its own streak). observeLiveIPs collects the due bans in
// Phase 1b and decides which can be applied this poll; enforceGeo, at the very
// end of PollOnce, lifts the suspensions whose time is up and applies the
// bans. Everything that writes goes through GeoSuspender, whose two methods
// are conditional writes: the suspension lands only on a row with no service
// reason, and the lift clears only a geo_auto written before its cutoff.

// Per-poll caps. Each applied transition pushes to every panel the user is on,
// inline, so an unbounded batch would stretch one poll arbitrarily; a mass
// event (or a broken location database with suspension on) is limited to
// twenty accounts per poll either way. Neither cap loses work: an over-cap ban
// keeps its streak at the threshold and fires on its next over-sample, and an
// over-cap lift is simply due again next poll.
//
// A cancelled poll is handled the same way, for the transitions it has not
// started: see enforceGeo.
const (
	geoMaxSuspensionsPerPoll = 20
	// Counted over DUE lifts only. Counting every geo_auto row would let
	// twenty 7-day suspensions that began earlier crowd a due 60-minute one
	// out of every poll for a week.
	geoMaxLiftsPerPoll = 20
)

// geoFollowUpWriteTimeout bounds the writes Phase 4 makes after the poll's
// context may already be cancelled: the audit row of a transition that has
// committed, and the re-armed streaks of the bans a cancelled poll did not
// get to. Both record something that has already happened, so they run
// detached from the cancellation (context.WithoutCancel), and both are one
// small statement, so they are bounded like one.
const geoFollowUpWriteTimeout = 30 * time.Second

// GeoSuspender is the narrow slice of user.Service the automatic suspension
// needs. Both methods are conditional and report whether they wrote, so a
// caller never mails, audits or counts a transition that another writer beat
// it to.
//
// applied=true with a non-nil error means the write happened and only the
// follow-up push failed and could not be queued; it is still a transition.
type GeoSuspender interface {
	// SuspendServiceIfClear writes reason/detail only while the row carries
	// no service reason at all.
	SuspendServiceIfClear(ctx context.Context, userID int64, reason domain.AutoDisabledReason, detail string) (bool, error)
	// LiftServiceIfHeldSince clears the service reason only while the FRESH
	// row still carries reason, written at or before suspendedAtOrBefore.
	LiftServiceIfHeldSince(ctx context.Context, userID int64, reason domain.AutoDisabledReason, suspendedAtOrBefore time.Time) (bool, error)
}

// SetGeoSuspender late-binds the automatic suspension's writer (user.Service
// in production). nil is supported and safe: due bans are counted as
// skipped_unwired and nothing is applied. Nothing else writes geo_auto (the
// admin API refuses it), so there is then nothing to lift either.
func (s *Service) SetGeoSuspender(g GeoSuspender) { s.geoSuspender = g }

// SetAuditRepo late-binds the audit log the automatic suspension writes its
// transitions to. nil = no audit rows; the transitions still happen, and the
// Warn log and the counter still record them.
func (s *Service) SetAuditRepo(a ports.AuditRepo) { s.audit = a }

// geoBan is one suspension the detector found due in Phase 1b, eligible at
// that moment and inside the per-poll cap. Tier and Spread feed the
// user-facing detail (which names no places); Reason is the admin-side text,
// which does, and goes to the log and the audit row.
//
// Record is the streak row Phase 1b saved for the user, with the ban streak
// the due verdict consumed. A ban Phase 4 does not start (the poll was
// cancelled) is deferred by saving it back with that streak re-armed, the way
// collectGeoBans defers an over-cap one; carrying the row saves a table load
// on that path and writes back exactly what this poll judged.
type geoBan struct {
	UserID int64
	Tier   domain.GeoTier
	Reason string
	Spread int
	Record domain.GeoRecord
}

// geoBanEligible: may the detector suspend this user now?
//
// Only an account whose service is actually active. No service reason at all
// (the detector never replaces anyone's decision, D3), and an AccessSnapshot
// that says active — which excludes, beyond the reasons, what is derived
// live: an expired account or one live over its quota would be re-labelled
// "suspended" (geo_auto outranks both in AccessSnapshot) and the real state
// hidden from the user and the admin, and an emergency window is a promise of
// service the detector has no standing to break. A disabled account has no
// service to suspend.
func geoBanEligible(u *domain.User, now time.Time) bool {
	return u != nil && u.ServiceDisabledReason == domain.DisabledNone &&
		u.AccessSnapshot(now).ServiceStatus == domain.ServiceStatusActive
}

// geoAutoSuspendDetail is the text the user reads in the portal and the
// suspension mail. Chinese, like every user-facing string the backend writes
// into a row, and deliberately naming NO place: the detector's evidence is a
// suspicion about the user's own connections, and the places stay on the admin
// side (log, audit, Geo tab). The minutes are the duration in effect when the
// suspension is applied; a later edit changes when it lifts, not this text.
func geoAutoSuspendDetail(tier domain.GeoTier, spread, minutes int) string {
	switch tier {
	case domain.GeoTierCountry:
		return fmt.Sprintf("检测到账号同时在 %d 个国家或地区使用，代理服务已临时暂停，约 %d 分钟后自动恢复", spread, minutes)
	case domain.GeoTierRegion:
		return fmt.Sprintf("检测到账号同时在同一国家的 %d 个省或州使用，代理服务已临时暂停，约 %d 分钟后自动恢复", spread, minutes)
	case domain.GeoTierCity:
		return fmt.Sprintf("检测到账号同时在同一国家的 %d 个城市使用，代理服务已临时暂停，约 %d 分钟后自动恢复", spread, minutes)
	default:
		// Unreachable from EvaluateGeo (a due ban always has a tier), but a
		// suspension must never carry an empty explanation.
		return fmt.Sprintf("检测到账号在多个地区同时使用，代理服务已临时暂停，约 %d 分钟后自动恢复", minutes)
	}
}

// geoAutoCount bumps one outcome of psp_geo_auto_suspension_total.
func geoAutoCount(outcome string, n int) {
	if n > 0 {
		metrics.GeoAutoSuspensionTotal.With(outcome).Add(int64(n))
	}
}

// enforceGeo is Phase 4 of PollOnce: lift the geo_auto suspensions whose time
// is up, then apply this cycle's bans.
//
// It runs at the END of the poll, after the metering flush: both transitions
// push to the panels inline and take the per-user lock ResyncMembership
// holds, and neither may delay persisting the cycle's traffic. It runs every
// cycle, whether or not any live-IP data was read, because a lift depends
// only on the clock.
//
// users is the poll's list, which the user loop has since updated in place
// (usage, rollovers, quota suspensions), so eligibility is re-checked on the
// freshest state the poll has; the writes themselves re-check the row.
//
// Lifts go first so a poll over its caps frees service before it takes more.
//
// A cancelled ctx (an admin closed the "Poll now" tab, the request timed out,
// the app is shutting down) stops NEW transitions; it is checked before each
// one. Every transition is inline pushes under the per-user lock, and its
// conditional write would fail on the dead context anyway, as a *_error that
// for a ban also loses the streak Phase 1b consumed. So a cancelled poll
// defers instead, with nothing lost: a due lift is due again next poll
// (lift_deferred), and a ban is re-armed exactly like an over-cap one
// (deferred). A transition already in flight when the cancellation lands is
// finished — the suspender detaches its push and retry queueing once its
// write has committed, and its audit row is written detached too.
func (s *Service) enforceGeo(ctx context.Context, users []*domain.User, bans []geoBan, pc *geoPolicyCache, now time.Time) {
	if s.geoSuspender == nil {
		// Nothing but the suspender can write geo_auto (the admin API
		// refuses it), so with none wired there is nothing to lift, and the
		// bans can only be counted. A Warn per poll that had something to
		// apply, not per ban.
		if len(bans) > 0 {
			geoAutoCount("skipped_unwired", len(bans))
			log.Warn("geo auto-suspension: suspensions are due but no suspender is wired; none applied", "due", len(bans))
		}
		return
	}
	if pc == nil {
		pc = s.newGeoPolicyCache(ctx, users)
	}
	s.liftDueGeoSuspensions(ctx, users, pc, now)
	s.applyGeoBans(ctx, users, bans, pc, now)
}

// liftDueGeoSuspensions ends the geo_auto suspensions whose time is up.
//
// The duration is the user's group's CURRENT effective one, whether or not
// suspension is still enabled there: turning it off stops new suspensions and
// must not strand the running ones. Due means ServiceDisabledAt + duration
// has passed, or no timestamp at all (a suspension that cannot be timed is
// due now rather than never).
//
// The poll's snapshot only nominates candidates. The decision is made by
// LiftServiceIfHeldSince on a fresh read under the per-user lock, against a
// cutoff of now - duration: a user an admin resumed and a concurrent poll
// re-suspended since this snapshot carries a newer timestamp and is refused.
func (s *Service) liftDueGeoSuspensions(ctx context.Context, users []*domain.User, pc *geoPolicyCache, now time.Time) {
	type dueLift struct {
		u *domain.User
		d time.Duration
	}
	var due []dueLift
	for _, u := range users {
		if u == nil || u.ServiceDisabledReason != domain.DisabledGeoAutoSuspend {
			continue
		}
		d := pc.forUser(u.ID).BanDuration()
		if u.ServiceDisabledAt != nil && now.Before(u.ServiceDisabledAt.Add(d)) {
			continue
		}
		due = append(due, dueLift{u: u, d: d})
	}
	// Longest-running first (an untimed row counts as the oldest), then by
	// ID, so an over-cap poll is deterministic and nobody waits twice.
	sort.Slice(due, func(i, j int) bool {
		a, b := due[i].u.ServiceDisabledAt, due[j].u.ServiceDisabledAt
		switch {
		case a == nil && b != nil:
			return true
		case a != nil && b == nil:
			return false
		case a != nil && b != nil && !a.Equal(*b):
			return a.Before(*b)
		}
		return due[i].u.ID < due[j].u.ID
	})
	if len(due) > geoMaxLiftsPerPoll {
		geoAutoCount("lift_deferred", len(due)-geoMaxLiftsPerPoll)
		due = due[:geoMaxLiftsPerPoll]
	}

	for i, c := range due {
		if err := ctx.Err(); err != nil {
			// The poll was cancelled: start no further lift. Each is due
			// again next poll, so nothing is lost; counted with the
			// over-cap ones because it is the same wait.
			n := len(due) - i
			geoAutoCount("lift_deferred", n)
			log.Info("geo auto-suspension: the poll was cancelled; the remaining due lifts wait for the next poll",
				"deferred", n, "err", err)
			break
		}
		u := c.u
		lifted, err := s.geoSuspender.LiftServiceIfHeldSince(ctx, u.ID, domain.DisabledGeoAutoSuspend, now.Add(-c.d))
		if !lifted {
			if err != nil {
				geoAutoCount("lift_error", 1)
				log.Warn("geo auto-suspension: lift failed; retried next poll", "user_id", u.ID, "err", err)
			} else {
				// An admin resumed or re-suspended the user since the
				// snapshot, or another poll lifted it first.
				geoAutoCount("lift_skipped", 1)
			}
			continue
		}
		if err != nil {
			log.Warn("geo auto-suspension lifted, but the config push failed and could not be queued",
				"user_id", u.ID, "err", err)
		}
		geoAutoCount("lifted_expiry", 1)
		minutes := int(c.d / time.Minute)
		log.Info("geo auto-suspension lifted", "user_id", u.ID, "duration_minutes", minutes)
		var suspendedAt any // JSON null when the row carried no timestamp
		if u.ServiceDisabledAt != nil {
			suspendedAt = u.ServiceDisabledAt.UTC().Format(time.RFC3339)
		}
		s.auditGeo(ctx, &domain.AuditEntry{
			Action:     "geo_auto_lift",
			Target:     geoAuditTarget(u),
			BeforeJSON: geoAuditJSON(map[string]any{"suspended_at": suspendedAt}),
			AfterJSON:  geoAuditJSON(map[string]any{"duration_minutes": minutes}),
			At:         now,
		})
		u.ServiceDisabledReason = domain.DisabledNone
		u.ServiceDisableDetail = ""
		u.ServiceDisabledAt = nil
	}
}

// applyGeoBans applies the bans Phase 1b collected, re-checking eligibility
// against the poll's updated users first: a user the quota suspended, or
// whose emergency window began, since Phase 1b is no longer a candidate.
//
// A failed write is counted and logged, and the ban is gone: the streak was
// consumed in Phase 1b and rebuilds over ban_after_polls further samples. The
// alternative — restoring it — would need a second streak write after the
// save, for an error path whose likeliest cause (the database) would fail
// that write too.
//
// A cancelled poll is not that error path. Its eligible bans are not
// attempted, and they ARE restored (rearmGeoBans): the cause is the caller
// going away, not the database, and the one write it takes runs detached.
// Held users are still consumed as skipped_held first, as in collectGeoBans.
func (s *Service) applyGeoBans(ctx context.Context, users []*domain.User, bans []geoBan, pc *geoPolicyCache, now time.Time) {
	if len(bans) == 0 {
		return
	}
	byID := make(map[int64]*domain.User, len(users))
	for _, u := range users {
		if u != nil {
			byID[u.ID] = u
		}
	}
	var rearm map[int64]domain.GeoRecord
	for _, b := range bans {
		u := byID[b.UserID]
		if !geoBanEligible(u, now) {
			geoAutoCount("skipped_held", 1)
			continue
		}
		if ctx.Err() != nil {
			// The same threshold the verdict was judged against, as in
			// collectGeoBans. In PollOnce this user's group was resolved
			// when Phase 1b judged them, so this is the poll's cached
			// answer, not a settings read on the cancelled context.
			rec := b.Record
			rec.Streak.BanOver = pc.forUser(u.ID).BanAfterPolls
			if rearm == nil {
				rearm = make(map[int64]domain.GeoRecord, len(bans))
			}
			rearm[u.ID] = rec
			continue
		}
		minutes := int(pc.forUser(u.ID).BanDuration() / time.Minute)
		detail := geoAutoSuspendDetail(b.Tier, b.Spread, minutes)
		applied, err := s.geoSuspender.SuspendServiceIfClear(ctx, u.ID, domain.DisabledGeoAutoSuspend, detail)
		if !applied {
			if err != nil {
				geoAutoCount("suspend_error", 1)
				log.Warn("geo auto-suspension: suspend failed; the ban streak rebuilds before it is due again",
					"user_id", u.ID, "tier", b.Tier, "err", err)
			} else {
				// The row gained a reason since the poll's read: someone
				// else's decision, which the detector does not overwrite.
				geoAutoCount("skipped_held", 1)
			}
			continue
		}
		if err != nil {
			log.Warn("geo auto-suspension applied, but the config push failed and could not be queued",
				"user_id", u.ID, "err", err)
		}
		geoAutoCount("suspended", 1)
		log.Warn("geo auto-suspension applied",
			"user_id", u.ID, "tier", b.Tier, "reason", b.Reason, "duration_minutes", minutes)
		s.auditGeo(ctx, &domain.AuditEntry{
			Action: "geo_auto_suspend",
			Target: geoAuditTarget(u),
			AfterJSON: geoAuditJSON(map[string]any{
				"tier": string(b.Tier), "reason": b.Reason, "duration_minutes": minutes,
			}),
			At: now,
		})
		u.ServiceDisabledReason = domain.DisabledGeoAutoSuspend
		u.ServiceDisableDetail = detail
		at := now
		u.ServiceDisabledAt = &at
	}
	if len(rearm) > 0 {
		geoAutoCount("deferred", len(rearm))
		log.Info("geo auto-suspension: the poll was cancelled; the remaining due suspensions are deferred to their next over-sample",
			"deferred", len(rearm), "err", ctx.Err())
		s.rearmGeoBans(ctx, rearm)
	}
}

// rearmGeoBans saves back the streak rows of bans a cancelled poll did not
// apply, their ban streak at the threshold again, so each user's next
// over-sample makes the ban due. It is the cap deferral's streak write, done
// after the save instead of before it, on a context detached from the
// cancellation that caused it.
//
// A failure is logged and the bans are lost the way a suspend_error loses
// one: the streak stays consumed and rebuilds over ban_after_polls samples.
func (s *Service) rearmGeoBans(ctx context.Context, rearm map[int64]domain.GeoRecord) {
	if s.geoStreaks == nil {
		// Nothing was persisted, so nothing was consumed across polls
		// either.
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), geoFollowUpWriteTimeout)
	defer cancel()
	if err := s.geoStreaks.Save(wctx, rearm); err != nil {
		log.Warn("geo auto-suspension: could not re-arm the deferred bans; their streaks rebuild before they are due again",
			"users", len(rearm), "err", err)
	}
}

// auditGeo writes one transition with the system actor, the way reconcile
// records its own runs. Best effort: the transition already happened, and a
// failed audit write must not undo or repeat it.
//
// Detached from the poll's cancellation (bounded by geoFollowUpWriteTimeout):
// a transition whose write committed just before the poll was cancelled is
// as real as any other, and its row is the only record of who changed the
// account and why.
func (s *Service) auditGeo(ctx context.Context, e *domain.AuditEntry) {
	if s.audit == nil {
		return
	}
	e.Actor = "geo-detector"
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), geoFollowUpWriteTimeout)
	defer cancel()
	if err := s.audit.Insert(actx, e); err != nil {
		log.Warn("geo auto-suspension: audit write failed", "action", e.Action, "target", e.Target, "err", err)
	}
}

func geoAuditTarget(u *domain.User) string {
	return fmt.Sprintf("user:%d %s", u.ID, u.UPN)
}

// geoAuditJSON marshals a small audit payload. A map, so the keys come out
// sorted and the rows diff cleanly; the values are strings, ints and nil,
// which cannot fail to marshal.
func geoAuditJSON(v map[string]any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
