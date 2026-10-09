package destpolicy

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

const (
	membershipCacheEntries = 64
	membershipCacheBudget  = 8 << 20
)

var errMembershipChanged = errors.New("membership changed during read")

type membershipInputs struct{ roster, quota RosterInput }
type membershipCacheEntry struct {
	key    string
	value  membershipInputs
	weight int
}
type membershipInputCache struct {
	mu     sync.Mutex
	items  map[string]*list.Element
	order  *list.List
	weight int
}

func newMembershipInputCache() *membershipInputCache {
	return &membershipInputCache{items: map[string]*list.Element{}, order: list.New()}
}
func (c *membershipInputCache) get(key string) (membershipInputs, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, found := c.items[key]
	if !found {
		return membershipInputs{}, false
	}
	c.order.MoveToFront(entry)
	return entry.Value.(membershipCacheEntry).value, true
}
func (c *membershipInputCache) put(key string, value membershipInputs) {
	// Conservative identity-only weight, not a claim about exact Go heap size.
	// Oversized results still count all members; they simply remain uncached.
	weight := 256 + 64*(len(value.roster.UserGroups)+len(value.quota.UserGroups)) + 8*(len(value.roster.UserIDs)+len(value.quota.UserIDs))
	if weight > membershipCacheBudget {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous, found := c.items[key]; found {
		c.weight -= previous.Value.(membershipCacheEntry).weight
		c.order.Remove(previous)
	}
	entry := membershipCacheEntry{key: key, value: value, weight: weight}
	c.items[key] = c.order.PushFront(entry)
	c.weight += weight
	for len(c.items) > membershipCacheEntries || c.weight > membershipCacheBudget {
		last := c.order.Back()
		old := last.Value.(membershipCacheEntry)
		delete(c.items, old.key)
		c.weight -= old.weight
		c.order.Remove(last)
	}
}

func membershipKey(panelID int64, generation uint64, ids []int64) string {
	hash := sha256.New()
	var value [8]byte
	for _, id := range ids {
		binary.BigEndian.PutUint64(value[:], uint64(id))
		_, _ = hash.Write(value[:])
	}
	return fmt.Sprintf("%d:%d:%x", panelID, generation, hash.Sum(nil))
}

func (i *Inputs) memberInputs(ctx context.Context, panelID int64, ids []int64) (membershipInputs, error) {
	if err := ctx.Err(); err != nil {
		return membershipInputs{}, err
	}
	if i.membershipGeneration == nil {
		return i.readMemberInputs(ctx, panelID, ids)
	}
	for range 3 {
		generation := i.membershipGeneration()
		key := membershipKey(panelID, generation, ids)
		if cached, found := i.membershipCache.get(key); found {
			if i.membershipGeneration() == generation {
				return cached, nil
			}
			continue
		}
		result := i.membershipFlights.DoChan(key, func() (any, error) {
			if cached, found := i.membershipCache.get(key); found {
				return cached, nil
			}
			loaded, err := i.readMemberInputs(ctx, panelID, ids)
			if err != nil {
				return nil, err
			}
			if i.membershipGeneration() != generation {
				return nil, errMembershipChanged
			}
			loaded.roster.MembershipGeneration, loaded.quota.MembershipGeneration = generation, generation
			loaded.roster.MembershipTracked, loaded.quota.MembershipTracked = true, true
			i.membershipCache.put(key, loaded)
			return loaded, nil
		})
		select {
		case <-ctx.Done():
			return membershipInputs{}, ctx.Err()
		case resolved := <-result:
			if errors.Is(resolved.Err, errMembershipChanged) {
				continue
			}
			if resolved.Err != nil {
				return membershipInputs{}, resolved.Err
			}
			if i.membershipGeneration() != generation {
				continue
			}
			return resolved.Val.(membershipInputs), nil
		}
	}
	return membershipInputs{}, fmt.Errorf("%w: membership did not settle during read", domain.ErrUnavailable)
}
