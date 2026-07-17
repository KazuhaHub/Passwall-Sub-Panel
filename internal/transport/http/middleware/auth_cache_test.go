package middleware

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestAuthUserCacheInvalidate(t *testing.T) {
	cache := NewAuthUserCache(2, time.Minute)
	cache.Put(authUserSnapshot{ID: 7, Role: domain.RoleAdmin, Enabled: true})
	if _, ok := cache.Get(7); !ok {
		t.Fatal("cached user was not found")
	}

	cache.Invalidate(7)
	cache.Invalidate(7) // missing entries are a no-op
	if _, ok := cache.Get(7); ok {
		t.Fatal("invalidated user remained cached")
	}
}

func TestAuthUserCacheEvictsLeastRecentlyUsed(t *testing.T) {
	cache := NewAuthUserCache(2, time.Minute)
	cache.Put(authUserSnapshot{ID: 1})
	cache.Put(authUserSnapshot{ID: 2})
	_, _ = cache.Get(1)
	cache.Put(authUserSnapshot{ID: 3})

	if _, ok := cache.Get(2); ok {
		t.Fatal("least recently used entry was not evicted")
	}
	if _, ok := cache.Get(1); !ok {
		t.Fatal("recently used entry was evicted")
	}
}
