package ports

import (
	"context"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type DestAuditRepo interface {
	ResolveDestinationAuditUsers(context.Context, []int64) (map[int64]bool, error)
	BeginDestinationAudit(context.Context, domain.DestAuditBatch) (domain.DestAuditBegin, error)
	WriteDestinationAuditChunk(context.Context, domain.DestAuditChunk) (string, error)
}

type DestAuditLossRepo interface {
	FlushDestinationAuditLoss(context.Context, domain.DestAuditLossBatch) error
}

type DestAuditControlRepo interface {
	// Seed the current native controls and register subsequent committed
	// changes atomically. The observer must only update bounded memory.
	WatchDestinationAuditControls(context.Context, func([]domain.DestAuditControl)) error
}

type DestAuditStore interface {
	DestAuditRepo
	DestAuditLossRepo
	DestAuditControlRepo
}
