package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// PerIPLimiter is a minimal fixed-window rate limiter keyed by client IP.
// Suitable for the project's friend-circle scale; swap for a token-bucket
// or a Redis-backed limiter if traffic grows.
type PerIPLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	limit   int
	window  time.Duration
}

type bucket struct {
	count  int
	expire time.Time
}

func NewPerIPLimiter(limitPerWindow int, window time.Duration) *PerIPLimiter {
	return &PerIPLimiter{
		buckets: make(map[string]*bucket),
		limit:   limitPerWindow,
		window:  window,
	}
}

// Allow returns true if the request from ip is within the limit.
func (l *PerIPLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok || now.After(b.expire) {
		l.buckets[ip] = &bucket{count: 1, expire: now.Add(l.window)}
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
}

// Handler returns a Gin middleware that 429s requests above the limit.
func (l *PerIPLimiter) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !l.Allow(c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit"})
			return
		}
		c.Next()
	}
}
