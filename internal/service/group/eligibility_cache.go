package group

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/boundedcache"
)

const eligibilityTTL = 30 * time.Second

type eligibilityEntry[T any] struct {
	value   T
	err     error
	done    chan struct{}
	loaded  bool
	expires time.Time
}
type eligibilityCache[T any] struct {
	mu      sync.Mutex
	entries *boundedcache.Cache[*eligibilityEntry[T]]
}

func newEligibilityCache[T any]() *eligibilityCache[T] {
	return &eligibilityCache[T]{entries: boundedcache.New[*eligibilityEntry[T]](512, 256<<10)}
}

func (c *eligibilityCache[T]) read(ctx context.Context, id int64, now func() time.Time, load func() (T, error)) (T, error) {
	var zero T
	if c == nil {
		return load()
	}
	key := strconv.FormatInt(id, 10)
	for range 3 {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		c.mu.Lock()
		entry, found := c.entries.Get(key)
		if found && !entry.loaded {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-entry.done:
			}
			if entry.err != nil {
				return zero, entry.err
			}
			continue
		}
		if found && now().Before(entry.expires) {
			value := entry.value
			c.mu.Unlock()
			return value, nil
		}
		entry = &eligibilityEntry[T]{done: make(chan struct{})}
		c.entries.Put(key, entry, 512)
		c.mu.Unlock()
		value, err := load()
		if err == nil {
			err = ctx.Err()
		}
		c.mu.Lock()
		current, retained := c.entries.Get(key)
		retained = retained && current == entry
		entry.value, entry.err, entry.loaded = value, err, true
		entry.expires = now().Add(eligibilityTTL)
		if err != nil && retained {
			c.entries.Delete(key)
		}
		close(entry.done)
		c.mu.Unlock()
		if err != nil {
			return zero, err
		}
		// Deletion/clear/eviction replaces the flight ticket. A read begun
		// before invalidation cannot return or repopulate the old facts.
		if retained {
			return value, nil
		}
	}
	return zero, domain.ErrUnavailable
}

func (c *eligibilityCache[T]) invalidate(id int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if id <= 0 {
		c.entries.Clear()
	} else {
		c.entries.Delete(strconv.FormatInt(id, 10))
	}
}
