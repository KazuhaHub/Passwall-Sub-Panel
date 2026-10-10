package ports

import (
	"context"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"time"
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

type DestAuditMaintenanceRepo interface {
	// A nil settings value skips configurable time retention; fixed dedup
	// retention and orphan removal still run. Counts describe committed rows.
	PruneDestinationAudit(context.Context, time.Time, *domain.DestinationSettings) (domain.DestAuditPruned, error)
}

type DestAuditReadRepo interface {
	// Read stored counters for the UTC hour buckets overlapping [since, until).
	// The window must be positive and no longer than 31 days. Only requested
	// positive panel IDs are returned; absent data has known zero counters and
	// complete=false. No destination or account values leave this projection.
	ReadDestinationAuditPanelStats(context.Context, time.Time, time.Time, []int64) (map[int64]domain.DestAuditPanelStats, error)
}

type DestHitReadRepo interface {
	ReadDestinationHits(context.Context, domain.DestHitQuery) (domain.DestHitPage, error)
}

type DestUserHitReadRepo interface {
	ReadDestinationUserHits(context.Context, int64, time.Time, time.Time) (domain.DestRecentHits, error)
}

type DestUsageReadRepo interface {
	ReadDestinationUsage(context.Context, domain.DestUsageQuery) (domain.DestUsagePage, error)
}

type DestAuditStore interface {
	DestAuditRepo
	DestAuditLossRepo
	DestAuditControlRepo
}
