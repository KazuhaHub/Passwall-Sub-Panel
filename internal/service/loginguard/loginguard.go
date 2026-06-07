// Package loginguard makes the pre-password decision for the local-login form:
// is this attempt currently locked out, and must it carry a valid captcha?
//
// It reads no settings of its own — the HTTP handler loads the live UISettings
// once per request (cheap, the settings repo is cached) and passes them in, so
// the guard stays a pure function of (settings, scope, failure history) and is
// trivial to test. The actual captcha *answer* check lives in the captcha
// package; this guard only decides whether a challenge is owed.
package loginguard

import (
	"context"
	"math"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// minutesToDuration converts a minutes setting to a Duration, saturating
// instead of overflowing. time.Duration is int64 nanoseconds, so a minutes
// value above ~153M (~292 years) would wrap negative — which here would
// silently disable the lock (until lands in the past, the count window lands
// in the future). Saturating keeps the feature fail-safe on a misconfigured
// huge value.
func minutesToDuration(m int) time.Duration {
	if m <= 0 {
		return 0
	}
	const maxMinutes = int64(math.MaxInt64 / int64(time.Minute))
	if int64(m) > maxMinutes {
		return time.Duration(maxMinutes) * time.Minute
	}
	return time.Duration(m) * time.Minute
}

// Decision is what the login handler should enforce before verifying the
// password. A zero Decision means "let the attempt proceed normally".
type Decision struct {
	// Locked is true when the scope has exceeded the lockout threshold and the
	// lock window has not yet elapsed; the handler must refuse the attempt.
	Locked bool
	// RetryAfter is how long until the lock lifts (only meaningful when Locked).
	RetryAfter time.Duration
	// CaptchaRequired is true when the attempt must include a valid captcha
	// response; the handler verifies it via the captcha package.
	CaptchaRequired bool
}

// Guard evaluates login attempts against the auth-event failure history.
type Guard struct {
	events ports.AuthEventRepo
	now    func() time.Time
}

// New builds a Guard over the given auth-event log.
func New(events ports.AuthEventRepo) *Guard {
	return &Guard{events: events, now: time.Now}
}

// Evaluate decides, before the password is checked, whether the attempt from
// (ip, upn) is locked and/or must present a captcha. It performs at most one
// DB read (the recent-failure count) and skips it entirely when neither
// feature needs it.
//
// Thresholds are taken verbatim from s — the settings layer is the single
// source of their defaults, so the guard never re-hardcodes them. A
// non-positive threshold is treated as "off" for that sub-decision (never as
// "always on"), so a misconfigured 0 fails safe.
func (g *Guard) Evaluate(ctx context.Context, s ports.UISettings, ip, upn string) (Decision, error) {
	var d Decision

	// "always"-mode captcha needs no history.
	if s.CaptchaEnabled && s.CaptchaTrigger == "always" {
		d.CaptchaRequired = true
	}

	needCount := s.LockoutEnabled ||
		(s.CaptchaEnabled && s.CaptchaTrigger == "after_failures")
	if !needCount {
		return d, nil
	}

	// Count over at least the lockout duration: a failure has to still be
	// inside the counting window for the lock to hold, so a duration longer
	// than the window would otherwise release the lock early.
	windowMin := s.LockoutWindowMinutes
	if s.LockoutEnabled && s.LockoutDurationMinutes > windowMin {
		windowMin = s.LockoutDurationMinutes
	}
	if windowMin <= 0 {
		return d, nil
	}

	scopeIP, scopeUPN := ip, upn
	if s.LockoutScope == "ip" {
		// IP-only scope: don't pin the username, so the count tracks the source
		// regardless of which account it's probing.
		scopeUPN = ""
	}

	since := g.now().Add(-minutesToDuration(windowMin))
	count, lastAt, err := g.events.RecentAuthFailures(ctx, scopeIP, scopeUPN, since)
	if err != nil {
		return Decision{}, err
	}

	if s.CaptchaEnabled && s.CaptchaTrigger == "after_failures" &&
		s.CaptchaFailThreshold > 0 && count >= int64(s.CaptchaFailThreshold) {
		d.CaptchaRequired = true
	}

	if s.LockoutEnabled && s.LockoutThreshold > 0 && count >= int64(s.LockoutThreshold) {
		until := lastAt.Add(minutesToDuration(s.LockoutDurationMinutes))
		if now := g.now(); now.Before(until) {
			d.Locked = true
			d.RetryAfter = until.Sub(now)
		}
	}

	return d, nil
}

// Evaluate2FA decides whether a user's second-factor verification is currently
// locked out. Unlike Evaluate (which is IP-scoped and pre-password), this counts
// failures PER ACCOUNT across all source IPs — the 2FA threat is a distributed
// attacker who already has the password grinding TOTP codes, which the per-IP
// login limiter and IP-scoped lockout can't stop. It reuses the same lockout
// thresholds as the password path (threshold / window / duration), so enabling
// account lockout covers the 2FA step too; with lockout off it's inert. Only the
// genuine AuthReason2FAInvalid failures are counted (not the guard's own
// 2fa_locked_out rejections), so the window can't slide forward forever.
func (g *Guard) Evaluate2FA(ctx context.Context, s ports.UISettings, userID int64) (Decision, error) {
	var d Decision
	if !s.LockoutEnabled || s.LockoutThreshold <= 0 {
		return d, nil
	}
	windowMin := s.LockoutWindowMinutes
	if s.LockoutDurationMinutes > windowMin {
		windowMin = s.LockoutDurationMinutes
	}
	if windowMin <= 0 {
		return d, nil
	}
	since := g.now().Add(-minutesToDuration(windowMin))
	count, lastAt, err := g.events.RecentUserFailures(ctx, userID, domain.AuthReason2FAInvalid, since)
	if err != nil {
		return Decision{}, err
	}
	if count >= int64(s.LockoutThreshold) {
		until := lastAt.Add(minutesToDuration(s.LockoutDurationMinutes))
		if now := g.now(); now.Before(until) {
			d.Locked = true
			d.RetryAfter = until.Sub(now)
		}
	}
	return d, nil
}
