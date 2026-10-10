package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

type usageAuditWriter interface {
	Insert(context.Context, *domain.AuditEntry) error
}
type usageAuditKey struct{ actor, user int64 }
type usageAuditRecord struct {
	expires time.Time
	done    chan struct{}
}

// Both read routes share this bounded audit admission cache. Pending inserts
// are shared by concurrent requests; a failed insert never grants admission.
type AdminDestinationUsageHandler struct {
	read    func(context.Context, domain.DestUsageQuery) (domain.DestUsagePage, error)
	audit   usageAuditWriter
	now     func() time.Time
	mu      sync.Mutex
	records map[usageAuditKey]*usageAuditRecord
}

func NewAdminDestinationUsageHandler(read func(context.Context, domain.DestUsageQuery) (domain.DestUsagePage, error), audit usageAuditWriter) *AdminDestinationUsageHandler {
	return &AdminDestinationUsageHandler{read: read, audit: audit, now: time.Now, records: make(map[usageAuditKey]*usageAuditRecord)}
}

func (h *AdminDestinationUsageHandler) Get(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	q, required, err := destinationUsageQuery(c.Request.URL.RawQuery, time.Now().UTC())
	if required {
		c.JSON(400, gin.H{"error": "dest_usage_user_required"})
		return
	}
	if err != nil {
		destinationHitQueryError(c)
		return
	}
	if page, ok := h.readAudited(c, q); ok {
		c.JSON(200, page)
	}
}

func (h *AdminDestinationUsageHandler) readAudited(c *gin.Context, q domain.DestUsageQuery) (domain.DestUsagePage, bool) {
	claims := middleware.ClaimsFrom(c)
	if claims == nil || claims.Role != domain.RoleAdmin || claims.UserID <= 0 {
		c.JSON(403, gin.H{"error": "forbidden"})
		return domain.DestUsagePage{}, false
	}
	if h.read == nil || h.audit == nil {
		respondPublicError(c, domain.ErrUnavailable)
		return domain.DestUsagePage{}, false
	}
	q, err := domain.NormalizeDestinationUsageQuery(q)
	if err != nil {
		destinationHitQueryError(c)
		return domain.DestUsagePage{}, false
	}
	page, err := h.read(c.Request.Context(), q)
	if err != nil {
		if errors.Is(err, domain.ErrValidation) {
			destinationHitQueryError(c)
		} else if errors.Is(err, domain.ErrNotFound) {
			destinationPolicyError(c, domain.ErrNotFound)
		} else {
			respondPublicError(c, domain.ErrUnavailable)
		}
		return domain.DestUsagePage{}, false
	}
	if h.record(c.Request.Context(), usageAuditKey{actor: claims.UserID, user: q.UserID}, claims.UPN, c.FullPath(), q) != nil {
		respondPublicError(c, domain.ErrUnavailable)
		return domain.DestUsagePage{}, false
	}
	return page, true
}

func (h *AdminDestinationUsageHandler) record(ctx context.Context, key usageAuditKey, actor, route string, q domain.DestUsageQuery) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := h.now().UTC()
		h.mu.Lock()
		if previous, ok := h.records[key]; ok {
			if previous.done != nil {
				done := previous.done
				h.mu.Unlock()
				select {
				case <-done:
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if now.Before(previous.expires) {
				h.mu.Unlock()
				return nil
			}
			delete(h.records, key)
		}
		if len(h.records) >= 4096 {
			for k, v := range h.records {
				if v.done == nil && !now.Before(v.expires) {
					delete(h.records, k)
				}
			}
			// Evicting an unexpired record would break ten-minute deduplication.
			if len(h.records) >= 4096 {
				h.mu.Unlock()
				return domain.ErrUnavailable
			}
		}
		pending := &usageAuditRecord{done: make(chan struct{})}
		h.records[key] = pending
		h.mu.Unlock()
		params, _ := json.Marshal(struct {
			UserID int64 `json:"user_id"`
			Since  int64 `json:"since"`
			Until  int64 `json:"until"`
		}{q.UserID, q.Since.UnixMilli(), q.Until.UnixMilli()})
		insertCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := h.audit.Insert(insertCtx, &domain.AuditEntry{Actor: actor, Action: "dest.usage.read", Target: route, BeforeJSON: string(params), At: now})
		cancel()
		h.mu.Lock()
		done := pending.done
		if err != nil {
			delete(h.records, key)
		} else {
			pending.expires = h.now().UTC().Add(10 * time.Minute)
			pending.done = nil
		}
		close(done)
		h.mu.Unlock()
		if err != nil {
			return domain.ErrUnavailable
		}
		return nil
	}
}

func destinationUsageQuery(raw string, now time.Time) (domain.DestUsageQuery, bool, error) {
	q := domain.DestUsageQuery{Since: now.Add(-24 * time.Hour), Until: now, Limit: 20}
	v, err := url.ParseQuery(raw)
	if err != nil {
		return q, false, domain.ErrValidation
	}
	if !v.Has("user_id") {
		return q, true, domain.ErrValidation
	}
	for key, values := range v {
		if len(values) != 1 || key != "user_id" && key != "panel_id" && key != "since" && key != "until" {
			return q, false, domain.ErrValidation
		}
	}
	for key, target := range map[string]*int64{"user_id": &q.UserID, "panel_id": &q.PanelID} {
		if v.Has(key) {
			n, e := strconv.ParseInt(v.Get(key), 10, 64)
			if e != nil || n <= 0 {
				return q, false, domain.ErrValidation
			}
			*target = n
		}
	}
	if v.Has("until") {
		q.Until, err = destinationHitTime(v.Get("until"), now, false)
		if err != nil {
			return q, false, err
		}
		q.Since = q.Until.Add(-24 * time.Hour)
	}
	if v.Has("since") {
		q.Since, err = destinationHitTime(v.Get("since"), q.Until, true)
		if err != nil {
			return q, false, err
		}
	}
	q, err = domain.NormalizeDestinationUsageQuery(q)
	return q, false, err
}

func destinationUserUsageQuery(raw string, userID int64, now time.Time) (*domain.DestUsageQuery, error) {
	v, err := url.ParseQuery(raw)
	if err != nil {
		return nil, domain.ErrValidation
	}
	if len(v) == 0 {
		return nil, nil
	}
	if len(v) != 1 || len(v["usage"]) != 1 {
		return nil, domain.ErrValidation
	}
	rangeValue := v.Get("usage")
	if rangeValue != "24h" && !strings.HasSuffix(rangeValue, "d") {
		return nil, domain.ErrValidation
	}
	since, err := destinationHitTime(rangeValue, now, true)
	if err != nil {
		return nil, err
	}
	q, err := domain.NormalizeDestinationUsageQuery(domain.DestUsageQuery{UserID: userID, Since: since, Until: now, Limit: 20})
	return &q, err
}
