package destaudit

import (
	"context"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"time"
)

type lossFlushResult struct {
	keys      int
	rows      int64
	discarded bool
}
type lossBuffer struct{}

func newLossBuffer() *lossBuffer                         { return &lossBuffer{} }
func (b *lossBuffer) add(loss domain.DestAuditLoss) bool { return false }
func (b *lossBuffer) flush(ctx context.Context, repo ports.DestAuditLossRepo, now time.Time) (lossFlushResult, error) {
	return lossFlushResult{}, nil
}
func (b *lossBuffer) discard() lossFlushResult { return lossFlushResult{} }
