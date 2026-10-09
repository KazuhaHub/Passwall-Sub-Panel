package ports

import (
	"context"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type DestAuditRepo interface {
	BeginDestinationAudit(context.Context, domain.DestAuditBatch) (domain.DestAuditBegin, error)
	WriteDestinationAuditChunk(context.Context, domain.DestAuditChunk) (string, error)
}
