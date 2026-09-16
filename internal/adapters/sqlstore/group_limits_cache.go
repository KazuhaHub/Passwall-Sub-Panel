package sqlstore

import (
	"context"
	"fmt"
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

// get returns one group's policy. A missing group or group ID of 0 resolves to
// "states nothing" (unlimited). A refresh failure serves a previously loaded
// value, but a cold-cache failure is returned: zero is a legitimate policy and
// must not be used to disguise "unknown". Callers then skip their operation,
// which leaves the last panel/node enforcement state intact.
func (c *groupLimitsCache) get(ctx context.Context, groupID int64) (domain.GroupLimits, error) {
	if groupID == 0 {
		return domain.GroupLimits{}, nil
	}
	c.mu.RLock()
	fresh := c.byGroup != nil && time.Since(c.loadedAt) < groupLimitsTTL
	if fresh {
		l := c.byGroup[groupID]
		c.mu.RUnlock()
		return l, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	// Re-check: another goroutine may have refreshed while we waited.
	if c.byGroup != nil && time.Since(c.loadedAt) < groupLimitsTTL {
		return c.byGroup[groupID], nil
	}
	var rows []groupRow
	if err := c.db.WithContext(ctx).Select("id", "traffic_limit_bytes", "ip_limit", "device_limit").
		Find(&rows).Error; err != nil {
		// Keep serving whatever we last had rather than failing every caller
		// on one blip. With no known value, propagate the failure instead of
		// fabricating the legitimate zero/unlimited policy.
		if c.byGroup != nil {
			return c.byGroup[groupID], nil
		}
		return domain.GroupLimits{}, fmt.Errorf("load group limits: %w", err)
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
	return m[groupID], nil
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
func (c *groupLimitsCache) resolve(ctx context.Context, u *domain.User) (*domain.User, error) {
	if u == nil {
		return nil, nil
	}
	limits, err := c.get(ctx, u.GroupID)
	if err != nil {
		return nil, err
	}
	eff := domain.ResolveLimits(u.Limits, limits)
	u.TrafficLimitBytes = eff.TrafficLimitBytes
	u.IPLimit = eff.IPLimit
	u.DeviceLimit = eff.DeviceLimit
	return u, nil
}

func (c *groupLimitsCache) resolveAll(ctx context.Context, us []*domain.User) ([]*domain.User, error) {
	for _, u := range us {
		if _, err := c.resolve(ctx, u); err != nil {
			return nil, err
		}
	}
	return us, nil
}
