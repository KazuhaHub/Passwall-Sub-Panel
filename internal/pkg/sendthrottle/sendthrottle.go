// Package sendthrottle bounds outbound verification / recovery emails against
// abuse with two independent gates checked together on every send:
//
//   - a per-recipient COOLDOWN (minimum gap between emails to the SAME account)
//     — stops an attacker bombing one victim's inbox via repeated
//     register/resend/forgot calls; and
//   - a panel-wide GLOBAL CAP (max emails per sliding window across ALL
//     accounts) — bounds total SMTP cost/quota even under distributed abuse
//     (many IPs, many distinct addresses) that the per-IP rate limiter and the
//     per-recipient cooldown individually can't see.
//
// It is the deliberate backstop behind the per-IP login limiter and the
// optional captcha: those gate the request, this gates the SIDE EFFECT (the
// email itself), so the protection holds no matter how the caller reaches the
// send path.
package sendthrottle

import (
	"sync"
	"time"
)

// maxKeys bounds the per-recipient map so a flood of distinct recipients can't
// grow it without limit; once exceeded, expired entries are swept.
const maxKeys = 8192

// Throttle is safe for concurrent use by multiple goroutines. The global cap is
// fixed at construction; the per-recipient cooldown is passed PER CALL so it can
// track a live admin setting without rebuilding the throttle.
type Throttle struct {
	mu           sync.Mutex
	globalLimit  int
	globalWindow time.Duration
	now          func() time.Time
	last         map[int64]time.Time // recipient key → last allowed send
	recent       []time.Time         // global send times within globalWindow
}

// New builds a Throttle. A non-positive globalLimit disables the global cap.
// now may be nil (defaults to time.Now) — tests inject a controllable clock.
func New(globalLimit int, globalWindow time.Duration, now func() time.Time) *Throttle {
	if now == nil {
		now = time.Now
	}
	return &Throttle{
		globalLimit:  globalLimit,
		globalWindow: globalWindow,
		now:          now,
		last:         make(map[int64]time.Time),
	}
}

// Allow reports whether an email to key may be sent now, applying the given
// per-recipient cooldown (non-positive disables that gate) and the throttle's
// global cap. When it returns true it atomically records the send against both
// gates; when it returns false nothing is recorded (a blocked send must not
// consume budget). Decision and record happen under one lock so concurrent
// callers can't both slip past.
func (t *Throttle) Allow(key int64, cooldown time.Duration) bool {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()

	// Per-recipient cooldown.
	if cooldown > 0 {
		if last, ok := t.last[key]; ok && now.Sub(last) < cooldown {
			return false
		}
	}
	// Global sliding-window cap.
	if t.globalLimit > 0 {
		t.pruneRecent(now)
		if len(t.recent) >= t.globalLimit {
			return false
		}
	}

	// Both gates passed → record.
	if cooldown > 0 {
		t.last[key] = now
		if len(t.last) > maxKeys {
			t.sweepKeys(now, cooldown)
		}
	}
	if t.globalLimit > 0 {
		t.recent = append(t.recent, now)
	}
	return true
}

// pruneRecent drops global send timestamps older than the window. recent is
// append-ordered, so the expired entries are a contiguous prefix.
func (t *Throttle) pruneRecent(now time.Time) {
	cutoff := now.Add(-t.globalWindow)
	i := 0
	for i < len(t.recent) && !t.recent[i].After(cutoff) {
		i++
	}
	if i > 0 {
		t.recent = t.recent[i:]
	}
}

// sweepKeys evicts per-recipient entries older than cooldown (they can no
// longer block anything), bounding the map's memory.
func (t *Throttle) sweepKeys(now time.Time, cooldown time.Duration) {
	cutoff := now.Add(-cooldown)
	for k, v := range t.last {
		if v.Before(cutoff) {
			delete(t.last, k)
		}
	}
}
