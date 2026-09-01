package sqlstore

import (
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// groupLimitsTTL is how stale a group's entitlement policy may be when
// resolving a user's effective limits.
//
// Correctness does not depend on this being small. A group policy is a
// standing decision, and the things that act on it are slow: the traffic poll
// runs on a multi-minute interval, the mailer hourly. Taking a few seconds to
// notice an edited policy is invisible next to those. What the cache buys is
// that resolving a user costs no query at all on the paths that load users
// constantly — every authenticated request goes through one.
const groupLimitsTTL = 30 * time.Second

// groupLimitsCache holds every group's entitlement policy, keyed by group ID.
//
// The whole table is loaded at once rather than per group: there are a handful
// of groups, so N+1 lookups would cost more than the single scan, and holding
// all of them means a user whose group was deleted resolves to "states
// nothing" without a second query to discover the absence.
type groupLimitsCache struct {
	db *gorm.DB

	mu       sync.RWMutex
	byGroup  map[int64]domain.GroupLimits
	loadedAt time.Time
}

func newGroupLimitsCache(db *gorm.DB) *groupLimitsCache {
	return &groupLimitsCache{db: db}
}

// get returns one group's policy. A missing group, an unreadable table, or a
// group ID of 0 all resolve to "states nothing", which resolves in turn to
// unlimited.
//
// Failing open is deliberate. The alternative — treating an unreadable policy
// as some limit — would cut off paying users because of a transient database
// error, and the panel-side cap is a safety net rather than the primary
// enforcement (PSP itself still meters and disables). Erring toward letting a
// user through is the right side to miss on.
func (c *groupLimitsCache) get(groupID int64) domain.GroupLimits {
	if groupID == 0 {
		return domain.GroupLimits{}
	}
	c.mu.RLock()
	fresh := c.byGroup != nil && time.Since(c.loadedAt) < groupLimitsTTL
	if fresh {
		l := c.byGroup[groupID]
		c.mu.RUnlock()
		return l
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	// Re-check: another goroutine may have refreshed while we waited.
	if c.byGroup != nil && time.Since(c.loadedAt) < groupLimitsTTL {
		return c.byGroup[groupID]
	}
	var rows []groupRow
	if err := c.db.Select("id", "traffic_limit_bytes", "ip_limit", "device_limit").
		Find(&rows).Error; err != nil {
		// Keep serving whatever we last had rather than flapping every
		// caller to unlimited on one blip; only a cold cache fails open.
		if c.byGroup != nil {
			return c.byGroup[groupID]
		}
		return domain.GroupLimits{}
	}
	m := make(map[int64]domain.GroupLimits, len(rows))
	for _, r := range rows {
		m[r.ID] = domain.GroupLimits{
			TrafficLimitBytes: r.TrafficLimitBytes,
			IPLimit:           r.IPLimit,
			DeviceLimit:       r.DeviceLimit,
		}
	}
	c.byGroup, c.loadedAt = m, time.Now()
	return m[groupID]
}

// invalidate drops the cache so the next read reloads. Called by the group
// repo on any write, so an operator editing a policy sees it take effect
// immediately rather than within the TTL.
func (c *groupLimitsCache) invalidate() {
	c.mu.Lock()
	c.byGroup, c.loadedAt = nil, time.Time{}
	c.mu.Unlock()
}

// resolve fills a user's effective entitlement fields from their stored
// overrides and their group's policy.
//
// Every path that turns a userRow into a domain.User goes through here. If a
// new one appears and forgets to, that user reads as unlimited on all three
// counts — which is why the repo funnels row mapping through resolveUsers
// rather than letting callers call toDomain directly.
func (c *groupLimitsCache) resolve(u *domain.User) *domain.User {
	if u == nil {
		return nil
	}
	eff := domain.ResolveLimits(u.Limits, c.get(u.GroupID))
	u.TrafficLimitBytes = eff.TrafficLimitBytes
	u.IPLimit = eff.IPLimit
	u.DeviceLimit = eff.DeviceLimit
	return u
}

func (c *groupLimitsCache) resolveAll(us []*domain.User) []*domain.User {
	for _, u := range us {
		c.resolve(u)
	}
	return us
}
