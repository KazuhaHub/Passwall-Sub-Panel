// Package audit centralises writes to the audit log so other services don't
// have to know about marshalling diffs or capturing the actor identity.
package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Service struct {
	repo ports.AuditRepo
}

func New(repo ports.AuditRepo) *Service { return &Service{repo: repo} }

// Record writes an audit entry with a structured before/after diff.
// Either before or after may be nil. Errors are logged but not propagated:
// audit failure should never break a user-facing write.
func (s *Service) Record(ctx context.Context, actor, action, target, ip string, before, after any) {
	entry := &domain.AuditEntry{
		Actor:      actor,
		Action:     action,
		Target:     target,
		BeforeJSON: jsonString(before),
		AfterJSON:  jsonString(after),
		IP:         ip,
		At:         time.Now(),
	}
	if err := s.repo.Insert(ctx, entry); err != nil {
		log.Warn("audit insert failed",
			"actor", actor, "action", action, "target", target, "err", err)
	}
}

func (s *Service) List(ctx context.Context, filter ports.AuditFilter) ([]*domain.AuditEntry, int64, error) {
	return s.repo.List(ctx, filter)
}

func jsonString(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
